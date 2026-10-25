// Package netair is what a check-in net's on-air settings (store.NetAir) mean
// on the mesh: the channel its messages go out on, the name they're sent as,
// and the channel messages logged to a session. Shared by net control's modem
// session (internal/core) and the MQTT ingester (internal/ingest), which both
// hear the net's channel.
package netair

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// MaxSender keeps the sender name short enough to leave most of a 160-byte
// message for the text.
const MaxSender = 32

// Channel resolves a net's channel, opening a private channel's key.
func Channel(air *store.NetAir, masterKey [32]byte) (mesh.Channel, error) {
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

// Sender is the name the net's messages are sent as: the on-air name, or the
// net's name made safe for it (no colon, which receivers split "Sender: text"
// on, and at most MaxSender bytes).
func Sender(air *store.NetAir, netName string) string {
	if air.OnAirName != "" {
		return air.OnAirName
	}
	name := strings.TrimSpace(strings.ReplaceAll(netName, ":", ""))
	for len(name) > MaxSender {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// Log is where a running session's channel messages go: the session, the
// channel to hear, the prefix that marks a message for the log, and the net's
// own sender name, whose messages repeated back by the mesh aren't logged.
type Log struct {
	SessionID int64
	Channel   mesh.Channel
	Prefix    string
	Sender    string
}

// Record logs raw if it's a message on the log's channel starting with its
// prefix, stored without the prefix. However many copies are heard — through
// the modem or any number of observers — it's logged once (see
// store.AddNetSessionMessage). added reports a new entry, with its sender and
// body.
func (l Log) Record(ctx context.Context, st *store.Store, raw []byte, heardAt time.Time) (sender, body string, added bool, err error) {
	if l.Prefix == "" {
		return "", "", false, nil
	}
	gt, ok := mesh.DecodeGroupText(raw, l.Channel)
	if !ok || gt.Sender == l.Sender {
		return "", "", false, nil
	}
	// The prefix only marks a message for the log; it isn't part of it.
	body, ok = strings.CutPrefix(gt.Text, l.Prefix)
	body = strings.TrimSpace(body)
	if !ok || body == "" {
		return "", "", false, nil
	}
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return "", "", false, nil
	}
	added, err = st.AddNetSessionMessage(ctx, l.SessionID, pkt.Payload, gt.Sender, body, heardAt)
	return gt.Sender, body, added, err
}
