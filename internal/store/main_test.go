package store

import (
	"os"
	"testing"

	"github.com/jleight/meshtender/internal/testdb"
)

// TestMain gives the package's DB-backed tests process-level setup/teardown of
// the testdb template and (when used) its container.
func TestMain(m *testing.M) {
	os.Exit(testdb.RunMain(m))
}
