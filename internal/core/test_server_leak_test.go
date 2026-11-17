package core

import (
	"net/http"
	"runtime"
	"testing"
	"time"
	"weak"
)

// TestFinishedTestServersAreCollectable guards the test helpers against pinning
// a finished test's server. The testing package keeps a parallel test's T alive
// until its parent finishes (for a top-level test, until the whole package does),
// and a T keeps reachable the closures it ran at cleanup. A helper whose cleanup
// still holds the server, or a background goroutine that's never stopped, so
// keeps every test's server (templates, auth, session stores; 30+ MB each) live
// for the whole run. Under -race that summed to over 16 GB and took the CI
// runner down.
//
// Parallel subtests build servers and keep only weak pointers. The parent's
// cleanup runs once they've finished but while their Ts are still held, so a
// server that stays reachable there is one the helpers leak.
func TestFinishedTestServersAreCollectable(t *testing.T) {
	const n = 3
	servers := make([]weak.Pointer[http.Server], n)
	t.Cleanup(func() {
		// Give goroutines still winding down (a closing connection, a pool
		// shutting down) a moment to let go: a leak stays reachable for good.
		deadline := time.Now().Add(5 * time.Second)
		for {
			runtime.GC()
			live := 0
			for _, p := range servers {
				if p.Value() != nil {
					live++
				}
			}
			if live == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d of %d servers are still reachable after their tests finished", live, n)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	for i := range n {
		t.Run("server", func(t *testing.T) {
			t.Parallel()
			_, _, ts, h := splitServer(t)
			// Serve a request too: a request's context points at its server,
			// so anything that keeps one alive keeps the server alive.
			do(t, ts, h.app, "/").Body.Close()
			servers[i] = weak.Make(ts.Config)
		})
	}
}
