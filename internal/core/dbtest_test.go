package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/testdb"
)

// appLogin creates a user and returns it plus a live app-host session cookie,
// established through the real /session/callback handoff. Endpoint tests pass the
// returned cookie to post()/do() to drive authenticated app routes.
func appLogin(t *testing.T, ts *httptest.Server, st *store.Store, ctx context.Context, host, username string) (*store.User, *http.Cookie) {
	t.Helper()
	u, err := st.CreateUser(ctx, username, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginID, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	code, err := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	resp := do(t, ts, host, "/session/callback?code="+code+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	resp.Body.Close()
	c := cookieByName(resp, "meshtender_session")
	if c == nil {
		t.Fatalf("no app session cookie after handoff for %q", username)
	}
	return u, c
}

// testConfig/testAuthConfig give the integration tests the app/auth/root hosts,
// using the same constants as splitServer (testAuthHost/testAppHost/testRootHost/
// testWWWHost, defined in handoff_test.go). Requests to a plain httptest listener
// arrive with Host 127.0.0.1, which matches none of these, so they route to the
// app surface by default — while the handoff/beacon still target real, distinct
// origins.
// testMasterKey is the master key for every DB-backed test. It's shared and fixed so
// the identity service and the config always agree, exactly as they do in production —
// harnesses used to seal the identity under a random key while leaving cfg.MasterKey
// zero, which silently broke anything decrypting through the config.
var testMasterKey = [32]byte{
	0x54, 0x65, 0x73, 0x74, 0x4d, 0x61, 0x73, 0x74,
	0x65, 0x72, 0x4b, 0x65, 0x79, 0x2d, 0x64, 0x6f,
	0x2d, 0x6e, 0x6f, 0x74, 0x2d, 0x75, 0x73, 0x65,
	0x2d, 0x69, 0x6e, 0x2d, 0x70, 0x72, 0x6f, 0x64,
}

func testConfig() *config.Config {
	return &config.Config{
		PrimaryHost: testAppHost, AuthHost: testAuthHost,
		RootHost: testRootHost, WWWHost: testWWWHost,
		MasterKey: testMasterKey,
	}
}

func testAuthConfig() auth.Config {
	return auth.Config{
		RPID: "localhost", RPDisplayName: "t",
		RPOrigins: []string{"http://" + testAuthHost, "http://" + testAppHost},
		AppHost:   testAppHost, AuthHost: testAuthHost, RootHost: testRootHost,
	}
}

// coreStore returns a Store backed by a fresh, throwaway database cloned from
// the migrated template (see internal/testdb). Each call is fully isolated —
// command_catalog seeded, everything else empty — so the integration tests need
// no truncation and don't share state.
func coreStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(ctx, testdb.Fresh(t, coreMigrate))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	return st, ctx
}

// coreMigrate applies the schema to the template database, releasing its
// connection before the template is cloned.
func coreMigrate(dsn string) error {
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.Migrate(ctx)
}

// TestMain wires process-level setup/teardown for the testdb template/container.
// It also shortens the packet-reply wait once, before any test runs: the
// integration tests run in parallel and only ever read perTryReply, so setting
// it here (rather than mutating it per-test) keeps it race-free under -race.
func TestMain(m *testing.M) {
	perTryReply = 300 * time.Millisecond
	os.Exit(testdb.RunMain(m))
}
