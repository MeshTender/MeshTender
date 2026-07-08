package core

import (
	"net/http"
	"strconv"
	"testing"
)

// These tests exercise the error paths of handlers that previously swallowed a
// store read error and rendered a misleading (data-wiping) page. Each test uses
// the per-test ephemeral DB to drop exactly the table the flagged call reads —
// the handler's earlier queries hit other tables and succeed, so only the flagged
// call fails. A swallowed error would render 200 with an empty form; the fix
// returns 500.

// TestPersonAccessReadErrorFailsClosed: if loading a share's granted command ids
// fails, the manage-access modal must 500 rather than render every box unchecked
// (which a subsequent Save would persist, wiping the target's real grants).
func TestPersonAccessReadErrorFailsClosed(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "grantowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Rep")
	target, err := st.CreateUser(ctx, "grantee", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, target.ID); err != nil {
		t.Fatal(err)
	}

	// Make ListShareCommandIDs (SELECT … FROM share_commands) fail; the earlier
	// lookups in the handler don't touch this table.
	if _, err := st.Pool().Exec(ctx, `DROP TABLE share_commands`); err != nil {
		t.Fatalf("drop share_commands: %v", err)
	}

	path := "/repeaters/" + rep.PublicID + "/share/" + strconv.FormatInt(target.ID, 10) + "/access"
	resp := do(t, ts, h.app, path, sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (a swallowed error would render 200 with an empty, data-wiping form)", resp.StatusCode)
	}
}

// TestRepeaterOrgLimitsReadErrorFailsClosed: if loading a repeater's per-org opt-in
// list fails, the limits modal must 500 rather than render as "permissive" (all
// checked), which a Save would persist as clearing the real restriction.
func TestRepeaterOrgLimitsReadErrorFailsClosed(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "limitowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Rep")
	org, err := st.CreateOrg(ctx, "Org", owner.ID) // creator is an admin member
	if err != nil {
		t.Fatal(err)
	}

	// Make RepeaterOrgOptInCommandIDs fail; the handler's earlier queries (org,
	// membership, catalog ceiling) use other tables.
	if _, err := st.Pool().Exec(ctx, `DROP TABLE org_repeater_command_optin`); err != nil {
		t.Fatalf("drop org_repeater_command_optin: %v", err)
	}

	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/orgs/"+org.Slug+"/limits", sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (a swallowed error would render 200 as permissive, wiping the restriction on save)", resp.StatusCode)
	}
}
