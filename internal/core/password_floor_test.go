package core

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/auth"
)

// TestPasswordFloorAppliesOnlyWhenSetting is the guarantee behind raising the
// minimum from 8 to 12 (audit S7): the floor is checked where a password is *set*,
// never where one is verified. An account whose password predates the increase keeps
// signing in; it only has to meet the current floor if the owner chooses a new
// password.
//
// This is the test that would catch someone "helpfully" adding ValidPassword to the
// login path, which would lock out every account with an older, shorter password.
func TestPasswordFloorAppliesOnlyWhenSetting(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	// A password that was fine under the old floor and is too short under the new one.
	const legacyPassword = "old8char" // 8 characters
	if len(legacyPassword) >= auth.MinPasswordLen {
		t.Fatalf("fixture password must be shorter than the floor (%d)", auth.MinPasswordLen)
	}

	// Establish it the way an existing account's hash exists today: through the auth
	// service's own setter, bypassing the form-level length check.
	svc, err := auth.New(st, st.Pool(), auth.Config{
		RPID: "localhost", RPDisplayName: "test",
		RPOrigins: []string{"http://auth.localhost"},
		AppHost:   testAppHost, AuthHost: testAuthHost, RootHost: testRootHost,
	})
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	legacyUser, err := st.CreateUser(ctx, "legacypw", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := svc.SetPassword(ctx, legacyUser.ID, legacyPassword); err != nil {
		t.Fatalf("set legacy password: %v", err)
	}

	t.Run("existing short password still signs in", func(t *testing.T) {
		resp := post(t, ts, h.auth, "/login/password", url.Values{
			"username": {"legacypw"},
			"password": {legacyPassword},
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("login = %d, want 303", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if strings.Contains(loc, "error=") {
			t.Fatalf("login failed for a pre-existing short password: %s", loc)
		}
		// A successful sign-in hands off with a code rather than bouncing back to /login.
		if !strings.Contains(loc, "/session/callback") {
			t.Fatalf("login didn't hand off; Location = %q", loc)
		}
	})

	t.Run("verification is unchanged for a wrong password", func(t *testing.T) {
		resp := post(t, ts, h.auth, "/login/password", url.Values{
			"username": {"legacypw"},
			"password": {"wrongwrongwrong"},
		})
		defer resp.Body.Close()
		if loc := resp.Header.Get("Location"); !strings.Contains(loc, "error=") {
			t.Fatalf("wrong password was accepted; Location = %q", loc)
		}
	})

	t.Run("signup rejects a password under the floor", func(t *testing.T) {
		resp := post(t, ts, h.auth, "/signup/password", url.Values{
			"username": {"tooshortpw"},
			"password": {strings.Repeat("a", auth.MinPasswordLen-1)},
		})
		defer resp.Body.Close()
		loc := resp.Header.Get("Location")
		if !strings.Contains(loc, "/signup?error=") {
			t.Fatalf("short signup password was accepted; Location = %q", loc)
		}
		if !strings.Contains(loc, strconv.Itoa(auth.MinPasswordLen)) {
			t.Errorf("the error should tell the user the required length (%d); Location = %q",
				auth.MinPasswordLen, loc)
		}
		// And no account may have been created.
		if _, err := st.GetUserByUsername(ctx, "tooshortpw"); err == nil {
			t.Error("a user was created despite the rejected password")
		}
	})

	t.Run("signup accepts a password at the floor", func(t *testing.T) {
		resp := post(t, ts, h.auth, "/signup/password", url.Values{
			"username": {"longenoughpw"},
			"password": {strings.Repeat("a", auth.MinPasswordLen)},
		})
		defer resp.Body.Close()
		if loc := resp.Header.Get("Location"); strings.Contains(loc, "error=") {
			t.Fatalf("a password at exactly the floor was rejected: %s", loc)
		}
	})
}

// TestPasswordFormsStateTheFloor checks the signup and account forms advertise the
// same minimum the server enforces — both in the human-readable hint and in the
// client-side minlength, which is what stops the browser submitting a doomed form.
func TestPasswordFormsStateTheFloor(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	want := strconv.Itoa(auth.MinPasswordLen)

	t.Run("signup form", func(t *testing.T) {
		resp := do(t, ts, h.auth, "/signup")
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		page := string(body)
		if !strings.Contains(page, "min "+want+" characters") {
			t.Errorf("signup form doesn't state a %s-character minimum", want)
		}
		if !strings.Contains(page, `minlength="`+want+`"`) {
			t.Errorf("signup password field lacks minlength=%q", want)
		}
	})

	t.Run("account change-password form", func(t *testing.T) {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar: %v", err)
		}
		seedSession(t, ts, st, ctx, jar, "pwformuser")
		// The account page lives on the auth host and needs that host's own session,
		// so drive it through the same handoff the app uses.
		resp := do(t, ts, h.auth, "/account", jar.Cookies(mustURL(t, ts.URL))...)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("account page = %d, want 200 — the handoff cookie should carry the "+
				"auth-host session", resp.StatusCode)
		}
		page := string(body)
		if !strings.Contains(page, "min "+want+" characters") {
			t.Errorf("account form doesn't state a %s-character minimum", want)
		}
		if !strings.Contains(page, `minlength="`+want+`"`) {
			t.Errorf("account password field lacks minlength=%q", want)
		}
	})
}
