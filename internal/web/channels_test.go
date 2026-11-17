package web

import (
	"encoding/hex"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestMeshCoreChannelURI: the deep link matches the MeshCore app's documented
// channel QR format (docs/qr_codes.md), with the region scope as a bare token —
// as the app's own Share Channel screen shows it — and none when unscoped.
func TestMeshCoreChannelURI(t *testing.T) {
	t.Parallel()
	const key = "8b3387e9c5cdea6ac9e5edbaa115cd72"
	if got, want := MeshCoreChannelURI("Public", key, ""), "meshcore://channel/add?name=Public&secret="+key; got != want {
		t.Errorf("unscoped = %q, want %q", got, want)
	}
	if got, want := MeshCoreChannelURI("#wny", key, "wny"), "meshcore://channel/add?name=%23wny&region_scope=wny&secret="+key; got != want {
		t.Errorf("scoped = %q, want %q", got, want)
	}
}

// TestHashtagChannelKey: a hashtag channel's key is derived from its exact name,
// matching MeshCore's documented example (#test) and the key the MeshCore app
// showed for #wny.
func TestHashtagChannelKey(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"#test": "9cd8fcf22a47333b591d96a2b848b73f", // docs/companion_protocol.md
		"#wny":  "ad145f5860c081d04dadd28f7a0b3cde", // the MeshCore app's Share Channel
	} {
		key, ok, err := channelKey(store.OrgChannel{Name: name}, [32]byte{}, false)
		if err != nil || !ok || hex.EncodeToString(key[:]) != want {
			t.Errorf("%s key = %x, %v, %v; want %s", name, key, ok, err, want)
		}
	}
	if _, ok, err := channelKey(store.OrgChannel{Name: "Ops", SecretSealed: []byte("sealed")}, [32]byte{}, false); ok || err != nil {
		t.Error("a private channel's key was opened for a viewer who may not see it")
	}
}

// TestUppercaseHashtag flags names members couldn't type in a companion app.
func TestUppercaseHashtag(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{"#lakeshore": false, "#Lakeshore": true, "#ÉCOLE": true, "#école-2": false} {
		if got := UppercaseHashtag(name); got != want {
			t.Errorf("UppercaseHashtag(%q) = %v, want %v", name, got, want)
		}
	}
}
