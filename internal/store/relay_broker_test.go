package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
)

func TestRelayBroker(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "relayadmin")
	member := mustUser(t, st, ctx, "relaymember")
	org := mustOrg(t, st, ctx, "RelayOrg", admin)
	if err := st.AddOrgMember(ctx, org, member, "member"); err != nil {
		t.Fatal(err)
	}
	observer := testKey(1)

	relayOrg := func(user int64) (int64, error) {
		t.Helper()
		b, err := st.RelayBroker(ctx, user, observer)
		if err != nil {
			return 0, err
		}
		return b.OrgID, nil
	}
	wantNone := func(why string, user int64) {
		t.Helper()
		if id, err := relayOrg(user); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got org %d, err %v; want ErrNotFound", why, id, err)
		}
	}

	wantNone("no observer or broker", admin)
	if err := st.CreateOrgObserver(ctx, org, "Roof", observer, admin); err != nil {
		t.Fatal(err)
	}
	wantNone("no broker", admin)
	broker := OrgBroker{OrgID: org, URL: "wss://mqtt.example.org/mqtt", Username: "mt", TopicPrefix: "meshcore", Enabled: true}
	if err := st.UpsertOrgBroker(ctx, broker, []byte("sealed"), false, admin); err != nil {
		t.Fatal(err)
	}
	if id, err := relayOrg(admin); err != nil || id != org {
		t.Fatalf("admin: org %d, err %v; want %d", id, err, org)
	}
	wantNone("a plain member", member)

	broker.Enabled = false
	if err := st.UpsertOrgBroker(ctx, broker, nil, false, admin); err != nil {
		t.Fatal(err)
	}
	wantNone("broker disabled", admin)
	broker.Enabled = true
	if err := st.UpsertOrgBroker(ctx, broker, nil, false, admin); err != nil {
		t.Fatal(err)
	}

	// A second org with the same observer: the one that heard it last wins.
	second := mustOrg(t, st, ctx, "SecondRelayOrg", admin)
	if err := st.CreateOrgObserver(ctx, second, "Shared", observer, admin); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertOrgBroker(ctx, OrgBroker{OrgID: second, URL: "wss://b.example.org/mqtt", TopicPrefix: "meshcore", Enabled: true}, nil, false, admin); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.TouchObserver(ctx, org, observer, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchObserver(ctx, second, observer, now); err != nil {
		t.Fatal(err)
	}
	if id, err := relayOrg(admin); err != nil || id != second {
		t.Fatalf("two orgs: org %d, err %v; want the more recent %d", id, err, second)
	}

	// An unapproved observer doesn't count.
	obs, err := st.ListOrgObservers(ctx, second, ObserversApproved)
	if err != nil || len(obs) != 1 {
		t.Fatalf("list: %v, %d", err, len(obs))
	}
	if err := st.UnapproveOrgObserver(ctx, second, obs[0].ID); err != nil {
		t.Fatal(err)
	}
	if id, err := relayOrg(admin); err != nil || id != org {
		t.Fatalf("after unapproving: org %d, err %v; want %d", id, err, org)
	}
}

func TestListRelayObservers(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "listrelayadmin")
	member := mustUser(t, st, ctx, "listrelaymember")
	org := mustOrg(t, st, ctx, "ListRelayOrg", admin)
	other := mustOrg(t, st, ctx, "ListRelayOther", admin)
	noBroker := mustOrg(t, st, ctx, "ListRelayNoBroker", admin)
	if err := st.AddOrgMember(ctx, org, member, "member"); err != nil {
		t.Fatal(err)
	}
	for _, o := range []int64{org, other} {
		b := OrgBroker{OrgID: o, URL: "wss://mqtt.example.org/mqtt", TopicPrefix: "meshcore", Enabled: true}
		if err := st.UpsertOrgBroker(ctx, b, nil, false, admin); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	add := func(orgID int64, name string, key int, firmware string, seen time.Time) {
		t.Helper()
		if err := st.CreateOrgObserver(ctx, orgID, name, testKey(key), admin); err != nil {
			t.Fatal(err)
		}
		if err := st.ReportObserver(ctx, orgID, testKey(key), "BUF", seen, &observe.Status{FirmwareVersion: firmware}); err != nil {
			t.Fatal(err)
		}
	}
	add(org, "Older", 1, "v1.18.0.4313-meshtender-6626f0c8", now.Add(-time.Hour))
	add(org, "Newer", 2, "v1.18.0.4313-meshtender-6626f0c8", now)
	add(org, "Upstream", 3, "v1.18.0.4300-observer-abcdef12", now)         // can't relay
	add(noBroker, "No broker", 4, "v1.18.0.4313-meshtender-6626f0c8", now) // org has no broker
	add(other, "Shared here", 1, "v1.18.0.4313-meshtender-6626f0c8", now)  // key 1 again, heard later
	add(other, "Unapproved", 5, "v1.18.0.4313-meshtender-6626f0c8", now)

	obs, err := st.ListOrgObservers(ctx, other, ObserversApproved)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range obs {
		if o.Name == "Unapproved" {
			if err := st.UnapproveOrgObserver(ctx, other, o.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	got, err := st.ListRelayObservers(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range got {
		names = append(names, o.Name+"@"+o.OrgName)
	}
	// Key 1 appears once, from the org that heard it last.
	want := []string{"Newer@ListRelayOrg", "Shared here@ListRelayOther"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("relay observers = %q, want %q", names, want)
	}
	if got, err := st.ListRelayObservers(ctx, member); err != nil || len(got) != 0 {
		t.Fatalf("plain member: %v, %v; want none", got, err)
	}
}
