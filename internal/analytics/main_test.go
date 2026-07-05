package analytics

import (
	"os"
	"testing"

	"github.com/jleight/meshtender/internal/testdb"
)

// TestMain wires testdb's container teardown. The container only starts if a test
// actually calls testdb.Fresh, so the nil-store handler tests stay DB-free.
func TestMain(m *testing.M) {
	os.Exit(testdb.RunMain(m))
}
