package netair

import (
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

func TestSender(t *testing.T) {
	t.Parallel()
	if got := Sender(&store.NetAir{OnAirName: "NC"}, "Sunday Net"); got != "NC" {
		t.Errorf("on-air name: %q", got)
	}
	if got := Sender(&store.NetAir{}, "Net: Sunday"); got != "Net Sunday" {
		t.Errorf("colon kept: %q", got)
	}
	long := strings.Repeat("é", 40)
	if got := Sender(&store.NetAir{}, long); len(got) > MaxSender || !strings.HasPrefix(long, got) {
		t.Errorf("long name: %q", got)
	}
}
