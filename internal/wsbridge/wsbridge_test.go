package wsbridge

import (
	"context"
	"testing"

	"github.com/meshcore-go/meshcore-go/hardware"
)

// TestFeedReassemblesAcrossChunkBoundaries verifies that inbound KISS frames
// are recovered no matter where the byte stream is split — including the case
// (a split immediately after a frame's trailing FEND) that made the SDK's
// ExtractFrames silently drop the following frame.
func TestFeedReassemblesAcrossChunkBoundaries(t *testing.T) {
	// Three back-to-back frames: a data frame and two hardware frames
	// (TX_DONE, RX_META) — exactly the mix a modem emits around a transmit.
	f1 := hardware.EncodeDataFrame([]byte{0x21, 0x00, 0x3d, 0x37, 0xaa, 0xbb, 0xcc})
	f2 := hardware.EncodeHardwareFrame(0, 0xF8, []byte{0x01})
	f3 := hardware.EncodeHardwareFrame(0, 0xF9, []byte{0x2f, 0xd7})

	stream := append(append(append([]byte{}, f1...), f2...), f3...)
	wantCmds := []byte{hardware.KISS_CMD_DATA, 0x06, 0x06}

	// Try every possible single split point, plus byte-by-byte.
	splitPoints := make([]int, 0, len(stream)+1)
	for i := 0; i <= len(stream); i++ {
		splitPoints = append(splitPoints, i)
	}

	for _, sp := range splitPoints {
		got := feedChunks(t, stream[:sp], stream[sp:])
		assertCmds(t, got, wantCmds, "split@%d", sp)
	}

	// Byte-by-byte (every boundary at once).
	var singles [][]byte
	for _, b := range stream {
		singles = append(singles, []byte{b})
	}
	got := feedChunks(t, singles...)
	assertCmds(t, got, wantCmds, "byte-by-byte")
}

func feedChunks(t *testing.T, chunks ...[]byte) []*hardware.KissFrame {
	t.Helper()
	c := New(context.Background(), nil) // Feed doesn't touch the websocket
	var got []*hardware.KissFrame
	c.SetFrameHandler(func(f *hardware.KissFrame) { got = append(got, f) })
	for _, ch := range chunks {
		c.Feed(ch)
	}
	return got
}

func assertCmds(t *testing.T, got []*hardware.KissFrame, wantCmds []byte, ctxFmt string, args ...any) {
	t.Helper()
	if len(got) != len(wantCmds) {
		t.Fatalf(ctxFmt+": got %d frames, want %d", append(args, len(got), len(wantCmds))...)
	}
	for i, f := range got {
		if f.Command != wantCmds[i] {
			t.Fatalf(ctxFmt+": frame %d cmd=0x%02x, want 0x%02x", append(args, i, f.Command, wantCmds[i])...)
		}
	}
}
