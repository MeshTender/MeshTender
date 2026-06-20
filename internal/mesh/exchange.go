package mesh

import (
	"context"
	"errors"
	"sync"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// ErrNoReply is returned by an exchange when no reply arrived after all retries.
var ErrNoReply = errors.New("mesh: no reply after retries")

// Sender is the subset of a KISS modem the exchanger needs.
type Sender interface {
	SendData([]byte) error
}

// Exchanger sends request packets to a single repeater and correlates replies,
// transparently retrying — a single LoRa packet (request out or reply back) is
// easily lost to collisions on a busy channel. It serializes sends through a
// rate limiter and stamps each (re)send with a fresh, strictly-increasing
// timestamp via SeqClock (required, or the repeater rejects a resend as a
// replay). It owns the modem's data handler: feed every received data frame to
// HandleData.
//
// Retry note: because retries use fresh timestamps, the repeater re-executes
// the command. This is safe for reads and idempotent settings; a non-idempotent
// command (e.g. advert) could run more than once if its reply is lost.
type Exchanger struct {
	sender   Sender
	server   meshcore.LocalIdentity
	repeater meshcore.Identity
	clock    *SeqClock
	limiter  *RateLimiter
	tries    int
	perTry   time.Duration

	mu     sync.Mutex
	active *pendingReply
	// Route learned from a login reply: once pathKnown, commands prefer direct
	// routing (only the repeaters on outPath relay them) and fall back to flood.
	pathKnown  bool
	outPath    []byte
	outPathLen byte
}

type pendingReply struct {
	match func(raw []byte) bool // returns true (and records result) if raw is our reply
	done  chan struct{}
}

// NewExchanger builds an Exchanger. interval is the minimum spacing between
// transmissions; perTry is how long to wait for a reply before resending;
// tries is the maximum number of sends.
func NewExchanger(sender Sender, server meshcore.LocalIdentity, repeater meshcore.Identity, interval, perTry time.Duration, tries int) *Exchanger {
	return &Exchanger{
		sender:   sender,
		server:   server,
		repeater: repeater,
		clock:    &SeqClock{},
		limiter:  NewRateLimiter(interval),
		tries:    tries,
		perTry:   perTry,
	}
}

// HandleData routes a received data frame to the in-flight request, if it
// matches. Frames received while no request is in flight, or that don't match
// (overheard mesh traffic), are ignored.
func (e *Exchanger) HandleData(raw []byte) {
	e.mu.Lock()
	p := e.active
	e.mu.Unlock()
	if p == nil {
		return
	}
	if p.match(raw) {
		e.mu.Lock()
		if e.active == p {
			e.active = nil
		}
		e.mu.Unlock()
		select {
		case p.done <- struct{}{}:
		default:
		}
	}
}

// exchange sends build(ts, attempt) and waits for a frame matchFn accepts,
// retrying up to e.tries times. build receives the 1-based attempt number so it
// can vary routing (e.g. direct early, flood as a fallback). onAttempt
// (optional) is called before each send. Returns ErrNoReply if exhausted, or
// ctx.Err() if cancelled.
func (e *Exchanger) exchange(ctx context.Context, build func(ts time.Time, attempt int) ([]byte, error), matchFn func([]byte) bool, onAttempt func(attempt, max int)) error {
	for attempt := 1; attempt <= e.tries; attempt++ {
		if onAttempt != nil {
			onAttempt(attempt, e.tries)
		}
		p := &pendingReply{match: matchFn, done: make(chan struct{}, 1)}
		e.mu.Lock()
		e.active = p
		e.mu.Unlock()

		pkt, err := build(e.clock.Next(), attempt)
		if err != nil {
			return err
		}
		if err := e.limiter.Wait(ctx); err != nil {
			return err
		}
		if err := e.sender.SendData(pkt); err != nil {
			return err
		}

		select {
		case <-p.done:
			return nil
		case <-time.After(e.perTry):
			e.mu.Lock()
			if e.active == p {
				e.active = nil
			}
			e.mu.Unlock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ErrNoReply
}

// Login authenticates to the repeater, returning the decoded reply. password
// may be empty for pubkey-ACL access.
func (e *Exchanger) Login(ctx context.Context, password string, onAttempt func(attempt, max int)) (*LoginResponse, error) {
	var result *LoginResponse
	err := e.exchange(ctx,
		func(ts time.Time, _ int) ([]byte, error) {
			// Login is always flood: it's the first contact, before any path is known.
			return BuildLoginPacket(e.server, e.repeater, password, ts)
		},
		func(raw []byte) bool {
			lr, err := DecodeLoginResponse(e.server, e.repeater, raw)
			if err != nil {
				return false
			}
			result = lr
			return true
		},
		onAttempt,
	)
	if err != nil {
		return nil, err
	}
	// Learn the route home so subsequent commands can use direct routing.
	e.mu.Lock()
	e.pathKnown = true
	e.outPath = result.OutPath
	e.outPathLen = result.OutPathLen
	e.mu.Unlock()
	return result, nil
}

// directThreshold is the highest attempt number that uses direct routing once a
// path is known; later attempts fall back to flood in case the path is stale.
func (e *Exchanger) directThreshold() int { return (e.tries + 1) / 2 }

// Command sends a CLI command and returns the repeater's reply text. Once a
// path has been learned (via Login), early attempts use direct routing and
// later attempts fall back to flood.
func (e *Exchanger) Command(ctx context.Context, command string, onAttempt func(attempt, max int)) (string, error) {
	var reply string
	err := e.exchange(ctx,
		func(ts time.Time, attempt int) ([]byte, error) {
			e.mu.Lock()
			direct := e.pathKnown && attempt <= e.directThreshold()
			path, pathLen := e.outPath, e.outPathLen
			e.mu.Unlock()
			if direct {
				return BuildCommandPacketDirect(e.server, e.repeater, command, ts, path, pathLen)
			}
			return BuildCommandPacket(e.server, e.repeater, command, ts)
		},
		func(raw []byte) bool {
			text, err := DecodeCommandReply(e.server, e.repeater, raw)
			if err != nil {
				return false
			}
			reply = text
			return true
		},
		onAttempt,
	)
	if err != nil {
		return "", err
	}
	return reply, nil
}
