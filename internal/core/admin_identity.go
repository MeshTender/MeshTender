package core

import (
	"errors"
	"net/http"

	"github.com/MeshTender/MeshTender/internal/identity"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// The server-identity backup page. The identity is a 32-byte seed sealed under
// MESHTENDER_MASTER_KEY; the backup is that same sealed blob wrapped in a
// self-describing envelope (see identity.ExportBackup). Because the ciphertext stays
// sealed under the master key, the exported string is safe to keep in a password
// manager and is useless to anyone who doesn't also hold the master key — which is why
// exporting needs no gate beyond the admin capability, and why a restore can't be fed
// an attacker-chosen identity (only a master-key holder can produce a valid envelope).

// pageIdentityBackup renders the current identity plus the export/restore controls. The
// backup itself is NOT rendered here — it appears only in response to an explicit POST,
// so merely opening the admin area doesn't put it on screen.
func (s *Handlers) pageIdentityBackup(w http.ResponseWriter, r *http.Request) {
	s.renderIdentityBackup(w, r, nil)
}

// renderIdentityBackup renders the page, merging in any result of an export/restore.
func (s *Handlers) renderIdentityBackup(w http.ResponseWriter, r *http.Request, extra map[string]any) {
	pub, _, err := s.Store.GetServerIdentity(r.Context())
	if errors.Is(err, store.ErrNoIdentity) {
		// Only reachable if the row vanished under a running process; the identity in
		// memory is still valid, so show that rather than an error page.
		pub = s.Identity.PublicKeyHex()
	} else if err != nil {
		s.ServerError(w, r, "could not load the server identity", err)
		return
	}
	data := map[string]any{
		"PublicKey": pub,
		// A mismatch means the process is running an identity the database no longer
		// holds — after a restore, until this replica restarts.
		"RunningPublicKey": s.Identity.PublicKeyHex(),
	}
	for k, v := range extra {
		data[k] = v
	}
	s.Render(w, r, "admin_identity.html", data)
}

// handleExportIdentity builds the backup envelope and shows it once.
//
// POST rather than GET so the value never lands in a URL, a browser history entry, or a
// page that merely got opened. (The app host is already no-store, so it won't be cached
// either.)
func (s *Handlers) handleExportIdentity(w http.ResponseWriter, r *http.Request) {
	pub, sealed, err := s.Store.GetServerIdentity(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load the server identity", err)
		return
	}
	// ExportBackup verifies the blob opens and derives pub before returning it, so a
	// corrupt row is caught here rather than on the day the backup is needed.
	envelope, err := identity.ExportBackup(s.Cfg.MasterKey, pub, sealed)
	if err != nil {
		web.LogError(r, "identity backup: export failed", err, "public_key", pub)
		s.renderIdentityBackup(w, r, map[string]any{
			"Error": "Could not produce a backup: this server's stored identity does not " +
				"decrypt under its configured master key. Do not rely on a backup until " +
				"that's resolved.",
		})
		return
	}
	// Worth an audit line: it records who took a copy of the (encrypted) identity.
	web.LogAudit(r, "identity backup: exported",
		"actor_user_id", s.Auth.CurrentUserID(r.Context()), "public_key", pub)
	s.renderIdentityBackup(w, r, map[string]any{"Backup": envelope})
}

// handleRestoreIdentity installs a pasted backup, refusing when that would orphan
// repeaters in the field. See store.ReplaceServerIdentityIfUnused for the rule.
func (s *Handlers) handleRestoreIdentity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	pasted := r.FormValue("backup")

	parsed, err := identity.ParseBackup(pasted)
	if err != nil {
		// A format error is the operator's typo, not a server fault, so it renders
		// inline rather than as a 500.
		s.renderIdentityBackup(w, r, map[string]any{
			"Error": "That doesn't look like a MeshTender identity backup. Paste the whole " +
				"value, starting with “meshtender-identity-v1.”.",
		})
		return
	}
	// Open it before writing anything: this both proves it decrypts under THIS server's
	// master key and cross-checks that the seed derives the public key on the label.
	if _, err := parsed.Open(s.Cfg.MasterKey); err != nil {
		web.LogError(r, "identity backup: restore rejected", err,
			"actor_user_id", s.Auth.CurrentUserID(r.Context()), "claimed_public_key", parsed.PublicKeyHex)
		s.renderIdentityBackup(w, r, map[string]any{
			"Error": "That backup could not be opened with this server's master key. Check " +
				"that MESHTENDER_MASTER_KEY matches the deployment the backup came from.",
		})
		return
	}

	outcome, err := s.Store.ReplaceServerIdentityIfUnused(r.Context(), parsed.PublicKeyHex, parsed.SealedSeed)
	if err != nil {
		s.ServerError(w, r, "could not restore the server identity", err)
		return
	}
	switch outcome {
	case store.RestoreAlreadyCurrent:
		s.renderIdentityBackup(w, r, map[string]any{
			"OK": "That backup holds the identity this server is already using. Nothing changed.",
		})
	case store.RestoreInstalled:
		web.LogAudit(r, "identity backup: restored",
			"actor_user_id", s.Auth.CurrentUserID(r.Context()), "public_key", parsed.PublicKeyHex)
		s.renderIdentityBackup(w, r, map[string]any{
			"OK": "Identity restored. Every running instance is still using the old identity " +
				"in memory — restart the deployment before adding or confirming repeaters.",
		})
	default: // store.RestoreRefusedInUse
		s.renderIdentityBackup(w, r, map[string]any{
			"Error": "Refused: this server already holds a different identity and has repeaters " +
				"registered against it. Installing another key would leave every one of those " +
				"repeaters granting admin to a key MeshTender no longer has, and each owner " +
				"would have to re-run setperm physically. Restore onto an empty deployment instead.",
		})
	}
}
