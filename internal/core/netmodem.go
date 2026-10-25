package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Net control's modem session: during a net, net control connects a MeshCore
// KISS modem to their browser (the same byte pipe as the repeater console) and
// MeshTender runs the net's side of the air through it.
//
//   - Receive: every advert goes through the same path as the MQTT ingester,
//     with MeshTender's own identity as the observer, so the modem counts as
//     one more observer. Channel messages on the net's channel that start with
//     its log prefix are logged to the session.
//   - Transmit: announcements and "Copied: @[name]" acknowledgements go out as
//     channel messages, one at a time — each waits for the modem's TX_DONE,
//     then for repeaters to have re-flooded it — so the net never floods the
//     mesh. Who has been acknowledged on the air is stored, so a reconnect
//     never repeats one.

// netModemTiming paces a modem session; tests shorten it.
type netModemTiming struct {
	// AckEvery is how often acknowledgements are batched on a net that
	// acknowledges automatically; ManualAckEvery is how soon net control's
	// Acknowledge click goes out on a manual one.
	AckEvery, ManualAckEvery time.Duration
	// MinGap is the least time between our transmissions; the gap also covers
	// netModemGapAirtimes airtimes of the last packet.
	MinGap time.Duration
	// TxTimeout bounds the wait for the modem's TX_DONE, which covers its
	// listen-before-talk wait as well as the transmission.
	TxTimeout time.Duration
	// Grace keeps the session open this long past the net session's end, so
	// net control can close the net on the air.
	Grace time.Duration
}

var defaultNetModemTiming = netModemTiming{
	AckEvery:       30 * time.Second,
	ManualAckEvery: 5 * time.Second,
	MinGap:         3 * time.Second,
	TxTimeout:      20 * time.Second,
	Grace:          15 * time.Minute,
}

// netModemGapAirtimes: a repeater re-floods after a random wait of up to
// 5 × airtime × tx_delay_factor (simple_repeater MyMesh::getRetransmitDelay),
// so waiting 6 airtimes after our own send leaves the first ring of repeaters
// room to repeat it before we key up again.
const netModemGapAirtimes = 6

// netModemLockBase namespaces the advisory lock that keeps one modem session
// per net session across replicas; the session id fills the low bits.
const netModemLockBase int64 = 0x4E43 << 48 // "NC"

// netModemRxQueue bounds packets waiting to be handled; a burst beyond it is
// dropped rather than stalling the modem.
const netModemRxQueue = 64

func (s *Handlers) netModemTiming() netModemTiming {
	if s.netModem != (netModemTiming{}) {
		return s.netModem
	}
	return defaultNetModemTiming
}

// netChannel resolves a net's channel, opening a private channel's key.
func netChannel(air *store.NetAir, masterKey [32]byte) (mesh.Channel, error) {
	switch air.ChannelKind {
	case store.ChannelHashtag:
		return mesh.HashtagChannel(air.ChannelName), nil
	case store.ChannelPrivate:
		psk, err := secretbox.Open(masterKey, air.PSKSealed)
		if err != nil || len(psk) != 16 {
			return mesh.Channel{}, fmt.Errorf("open channel key: %w", err)
		}
		return mesh.PrivateChannel([16]byte(psk)), nil
	default:
		return mesh.PublicChannel(), nil
	}
}

// onAirSender is the name the net's messages are sent as: the on-air name, or
// the net's name made safe for it (no colon, which receivers split
// "Sender: text" on, and short enough to leave room for the text).
func onAirSender(air *store.NetAir, netName string) string {
	if air.OnAirName != "" {
		return air.OnAirName
	}
	name := strings.TrimSpace(strings.ReplaceAll(netName, ":", ""))
	for len(name) > maxOnAirName {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// ackName is how an acknowledgement names a member: a mention of the
// companion as it advertises itself, which companion apps highlight, or the
// member's name when MeshTender never heard an advert (added by hand).
func ackName(e *store.RosterEntry) string {
	if e.CompanionAdvertName != "" {
		return "@[" + e.CompanionAdvertName + "]"
	}
	return e.MemberName
}

// ackMessages packs members into as few "Copied: …" messages as fit.
func ackMessages(sender string, entries []*store.RosterEntry) []netModemTx {
	const prefix = "Copied: "
	room := mesh.GroupTextRoom(sender)
	var out []netModemTx
	var cur *netModemTx
	for _, e := range entries {
		name := ackName(e)
		if cur != nil && len(cur.text)+len(", ")+len(name) <= room {
			cur.text += ", " + name
			cur.members = append(cur.members, e.MemberID)
			continue
		}
		out = append(out, netModemTx{text: prefix + name, members: []int64{e.MemberID}})
		cur = &out[len(out)-1]
	}
	return out
}

// netModemTx is one channel message to send. members are marked acknowledged
// on the air once it's sent.
type netModemTx struct {
	text    string
	members []int64
}

// netModemRun is one connected modem session.
type netModemRun struct {
	s      *Handlers
	sess   *modemSession
	ctx    context.Context
	c      netSessionContext
	air    *store.NetAir
	ch     mesh.Channel
	scope  *meshcore.RegionKey
	sender string
	timing netModemTiming
	// airtime estimates a packet's time on air in ms, for transmit spacing.
	airtime func(packetLen int) uint32
	tx      chan netModemTx

	mu       sync.Mutex
	inFlight map[int64]bool // members queued for an acknowledgement
}

func (r *netModemRun) status(state, msg string) { _ = r.sess.Bridge.Status(state, msg) }

// wsNetModem is the net control modem WebSocket. Net control only, while the
// session is in progress and the org's radio is set.
func (s *Handlers) wsNetModem(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadNetSession(w, r)
	if !ok {
		return
	}
	if !c.netControl {
		s.NotFound(w, r)
		return
	}
	ctx := r.Context()
	now := time.Now()
	timing := s.netModemTiming()
	// Problems past this point are reported over the socket: a browser can't
	// read the body of a failed WebSocket upgrade.
	var problem string
	air, err := s.Store.GetNetAir(ctx, c.org.ID, c.net.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the on-air settings.", err)
		return
	}
	radio, err := s.Store.OrgRadio(ctx, c.org.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the radio settings.", err)
		return
	}
	ch, err := netChannel(air, s.Cfg.MasterKey)
	if err != nil {
		s.ServerError(w, r, "Couldn't open the channel key.", err)
		return
	}
	var scope *meshcore.RegionKey
	if air.ScopeToken != "" {
		key, err := mesh.RegionScope(air.ScopeToken)
		if err != nil {
			s.ServerError(w, r, "Couldn't scope to the region.", err)
			return
		}
		scope = &key
	}
	switch {
	case !now.Before(c.session.EndsAt):
		problem = "This session is over."
	case radio == nil:
		problem = "The organization's radio settings aren't set. An admin sets them on the Configuration tab."
	}
	var lock *store.SessionLock
	if problem == "" {
		if lock, err = s.Store.TryAcquireSessionLock(ctx, netModemLockBase|c.session.ID); err != nil {
			s.ServerError(w, r, "Couldn't start the modem session.", err)
			return
		}
		if lock == nil {
			problem = "Net control already has a modem connected to this session."
		}
	}
	if lock != nil {
		defer func() {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			lock.Release(rctx)
		}()
	}

	sess, ok := s.openModemSession(w, r, time.Until(c.session.EndsAt)+timing.Grace, hardware.WithTxFlowControl(timing.TxTimeout))
	if !ok {
		return
	}
	defer sess.Close()
	if problem != "" {
		_ = sess.Bridge.Status("error", problem)
		return
	}

	run := &netModemRun{
		s: s, sess: sess, ctx: sess.Ctx, c: c, air: air, ch: ch, scope: scope,
		sender: onAirSender(air, c.net.Name), timing: timing,
		airtime:  airtimeEstimator(*radio),
		tx:       make(chan netModemTx, 16),
		inFlight: map[int64]bool{},
	}
	rx := make(chan []byte, netModemRxQueue)
	sess.Modem.SetDataHandler(func(data []byte, _ float32, _ int8, _ bool) {
		select {
		case rx <- append([]byte(nil), data...):
		default: // a burst beyond the queue is dropped, not allowed to stall the modem
		}
	})
	if err := sess.Modem.Connect(sess.Ctx); err != nil {
		web.LogError(r, "net modem: connect", err, "session_id", c.session.ID)
		run.status("error", "modem connect: "+err.Error())
		return
	}
	sess.Start()
	if !sess.WaitReady() {
		return
	}
	if err := sess.Modem.SetRadio(&hardware.RadioConfig{FreqHz: radio.FreqHz, BwHz: radio.BwHz, SF: radio.SF, CR: radio.CR}); err != nil {
		run.status("error", "set radio: "+err.Error())
		return
	}
	run.status("ready", fmt.Sprintf("Listening on %s MHz. Sending on %s as %q.", radio.MHz(), channelLabel(air), run.sender))

	go run.receive(rx)
	go run.transmit()
	go run.acknowledge()
	for {
		select {
		case <-sess.Ctx.Done():
			return
		case msg := <-sess.Messages():
			run.announce(msg)
		}
	}
}

// channelLabel names a net's channel for net control.
func channelLabel(air *store.NetAir) string {
	switch air.ChannelKind {
	case store.ChannelHashtag:
		return "#" + air.ChannelName
	case store.ChannelPrivate:
		if air.ChannelName != "" {
			return air.ChannelName + " (private)"
		}
		return "a private channel"
	default:
		return "Public"
	}
}

// airtimeEstimator estimates time on air for the org's radio. meshcore-go's
// estimator takes the coding rate as 1–4 (4/5–4/8); `set radio` takes 5–8.
func airtimeEstimator(radio config.RadioDefaults) func(int) uint32 {
	return hardware.LoRaAirtimeEstimator(&hardware.RadioConfig{
		FreqHz: radio.FreqHz, BwHz: radio.BwHz, SF: radio.SF, CR: radio.CR - 4,
	})
}

// announce handles net control's buttons: open, close, or a message of their own.
func (r *netModemRun) announce(msg modemClientMsg) {
	var text string
	switch msg.Type {
	case "open":
		text = r.c.net.Name + " is open. Send a flood advert to check in."
	case "close":
		roster, err := r.s.Store.NetSessionRoster(r.ctx, r.c.org.ID, r.c.session)
		if err != nil {
			r.status("error", "couldn't count check-ins")
			return
		}
		n := 0
		for i := range roster {
			if roster[i].CheckedIn() {
				n++
			}
		}
		text = fmt.Sprintf("%s is closed: %d checked in. Thanks, all.", r.c.net.Name, n)
	case "say":
		text = strings.TrimSpace(msg.Text)
		if text == "" || !plainText(text) {
			r.status("error", "Type a message to send.")
			return
		}
	default:
		return
	}
	r.queue(netModemTx{text: text})
}

func (r *netModemRun) queue(t netModemTx) {
	select {
	case r.tx <- t:
		r.status("queued", "Queued: "+mesh.FitGroupText(r.sender, t.text))
	default:
		r.status("error", "Too many messages waiting to go out — try again shortly.")
		r.release(t.members)
	}
}

func (r *netModemRun) release(members []int64) {
	r.mu.Lock()
	for _, id := range members {
		delete(r.inFlight, id)
	}
	r.mu.Unlock()
}

// transmit sends queued messages one at a time: each waits for the modem's
// TX_DONE, then for the mesh to have repeated it, before the next.
func (r *netModemRun) transmit() {
	for {
		var t netModemTx
		select {
		case <-r.ctx.Done():
			return
		case t = <-r.tx:
		}
		raw, err := mesh.BuildGroupText(r.ch, r.sender, t.text, r.scope, time.Now())
		if err == nil {
			err = r.sess.Modem.SendData(raw)
		}
		switch {
		case err == nil:
			if len(t.members) > 0 {
				if err := r.s.Store.MarkAired(r.ctx, r.c.session.ID, t.members, time.Now()); err != nil {
					r.status("error", "Sent, but couldn't record who was acknowledged.")
				}
			}
			r.status("sent", "Sent: "+mesh.FitGroupText(r.sender, t.text))
		case errors.Is(err, hardware.ErrTxBusy), errors.Is(err, hardware.ErrTxFailed), errors.Is(err, hardware.ErrTxTimeout):
			r.status("error", "The modem couldn't send: "+mesh.FitGroupText(r.sender, t.text))
		default:
			r.status("error", "send: "+err.Error())
		}
		// Unsent acknowledgements are retried on the next pass.
		r.release(t.members)
		gap := time.Duration(r.airtime(len(raw))) * time.Millisecond * netModemGapAirtimes
		if gap < r.timing.MinGap {
			gap = r.timing.MinGap
		}
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(gap):
		}
	}
}

// acknowledge periodically queues "Copied: …" for members who should be
// acknowledged on the air and haven't been: everyone checked in on an
// automatic net, the ones net control acknowledged on a manual one.
func (r *netModemRun) acknowledge() {
	every := r.timing.AckEvery
	if r.c.net.ManualAck {
		every = r.timing.ManualAckEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-tick.C:
		}
		roster, err := r.s.Store.NetSessionRoster(r.ctx, r.c.org.ID, r.c.session)
		if err != nil {
			continue
		}
		var due []*store.RosterEntry
		r.mu.Lock()
		for i := range roster {
			e := &roster[i]
			ready := e.CheckedIn()
			if r.c.net.ManualAck {
				ready = e.Acked()
			}
			if ready && !e.Aired() && !r.inFlight[e.MemberID] {
				r.inFlight[e.MemberID] = true
				due = append(due, e)
			}
		}
		r.mu.Unlock()
		for _, t := range ackMessages(r.sender, due) {
			r.queue(t)
		}
	}
}

// receive handles what the modem hears: adverts become check-ins, prefixed
// channel messages are logged.
func (r *netModemRun) receive(rx <-chan []byte) {
	observer := r.s.Identity.PublicKeyHex()
	for {
		var raw []byte
		select {
		case <-r.ctx.Done():
			return
		case raw = <-rx:
		}
		now := time.Now()
		adv, companionID, err := ingest.RegisteredAdvert(r.ctx, r.s.Store, raw)
		if err == nil {
			created, err := ingest.RecordAdvert(r.ctx, r.s.Store, adv, companionID, observer, now)
			switch {
			case errors.Is(err, ingest.ErrAdvertSkew):
				r.status("warning", fmt.Sprintf("Heard %s, but its clock is off, so it didn't count.", adv.Name))
			case err == nil && created:
				r.status("heard", "Heard "+adv.Name+" check in.")
			}
			continue
		}
		r.logMessage(raw, now)
	}
}

// logMessage logs a channel message on the net's channel that starts with the
// log prefix, without the prefix — except our own, repeated back to us by the
// mesh.
func (r *netModemRun) logMessage(raw []byte, now time.Time) {
	if r.air.LogPrefix == "" {
		return
	}
	gt, ok := mesh.DecodeGroupText(raw, r.ch)
	if !ok || gt.Sender == r.sender {
		return
	}
	// The prefix only marks a message for the log; it isn't part of it.
	body, ok := strings.CutPrefix(gt.Text, r.air.LogPrefix)
	body = strings.TrimSpace(body)
	if !ok || body == "" {
		return
	}
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return
	}
	added, err := r.s.Store.AddNetSessionMessage(r.ctx, r.c.session.ID, pkt.Payload, gt.Sender, body, now)
	if err == nil && added {
		r.status("message", gt.Sender+": "+body)
	}
}
