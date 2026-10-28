// Package core is the application (app host) surface — the authenticated product
// — and the composition root that assembles the auth, marketing, and core
// surfaces behind the host dispatcher.
package core

import (
	"context"
	"embed"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/auth"
	"github.com/MeshTender/MeshTender/internal/buildinfo"
	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/identity"
	"github.com/MeshTender/MeshTender/internal/marketing"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Handlers carries the shared environment plus the auth service for the app
// surface's HTTP handlers.
type Handlers struct {
	*web.Env
	Auth *auth.Service

	// wsCtx is the base context for WebSocket handlers; cancelling it (on shutdown)
	// signals active console sockets to close. wsWG tracks those handlers so
	// shutdown can wait for them — http.Server.Shutdown does not close hijacked
	// (WebSocket) connections.
	wsCtx    context.Context
	wsCancel context.CancelFunc
	wsWG     sync.WaitGroup

	// netModem paces net control modem sessions; zero means the defaults.
	netModem netModemTiming
}

// Server is the built HTTP entry point.
type Server struct {
	handler http.Handler
	app     *Handlers
	csp     *web.CSPCollector
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// CollectCSPReports runs the violation-report writer until ctx is canceled. Start
// it in a goroutine alongside the other background workers, and let it finish before
// the store's pool closes — it does a final flush of anything still queued.
func (s *Server) CollectCSPReports(ctx context.Context) { s.csp.Run(ctx) }

// WSDrainTimeout is the recommended deadline for DrainWebSockets on shutdown. It
// must comfortably exceed a single handler's consoleEndTimeout (the deferred
// EndConsoleSession stamp) plus the time a handler needs to unwind after its
// context is canceled — otherwise the drain gives up mid-stamp and leaves the
// session's ended_at NULL, so it shows "in progress" forever. It must also fit
// inside the deployment's process stop grace (Kubernetes default 30s), alongside
// the preceding HTTP drain. TestDrainTimeoutExceedsStamp enforces the lower bound.
const WSDrainTimeout = 15 * time.Second

// DrainWebSockets closes active WebSocket connections and waits for their handlers
// to return, up to ctx's deadline. Call it after http.Server.Shutdown, which does
// not close hijacked/WebSocket connections. Reports whether all handlers finished
// before the deadline.
func (s *Server) DrainWebSockets(ctx context.Context) bool {
	s.app.wsCancel()
	done := make(chan struct{})
	go func() {
		s.app.wsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// NewServer assembles every surface and the host dispatcher. The deployment is
// always split-host (auth / root / app); the auth host serves sign-in + account,
// the root host serves marketing + public org discovery, and the app host (plus
// custom org domains and any unrecognized host) serves the product.
func NewServer(st *store.Store, authSvc *auth.Service, idSvc *identity.Service, cfg *config.Config) (*Server, error) {
	// One collector for all three surfaces: violations happen on every host but
	// aggregate into a single table, and a shared collector also means one shared
	// per-IP rate limit rather than three that each allow a full burst.
	csp := web.NewCSPCollector(st, cfg)
	deps := web.Deps{
		Store:    st,
		Identity: idSvc,
		Cfg:      cfg,
		CSP:      csp,
		UserInfo: func(ctx context.Context) (string, bool, string, bool) {
			uid := authSvc.CurrentUserID(ctx)
			if uid == 0 {
				return "", false, "", false
			}
			u, err := st.GetUserByID(ctx, uid)
			if err != nil {
				return "", false, "", false
			}
			return u.Name(), u.CapManageUsers || u.CapManageCatalog, u.Timezone, true
		},
		LookupTXT: net.LookupTXT,
		// Read once here rather than per request: it hashes the executable, and
		// the answer can't change while the process runs. Sharing one value also
		// guarantees the public endpoint and the admin page can never disagree.
		Build: buildinfo.Read(cfg.ImageDigest),
	}

	coreEnv, err := web.NewEnv(deps, templatesFS)
	if err != nil {
		return nil, err
	}
	app := &Handlers{Env: coreEnv, Auth: authSvc}
	app.wsCtx, app.wsCancel = context.WithCancel(context.Background())

	authH, err := auth.NewWeb(deps, authSvc)
	if err != nil {
		return nil, err
	}
	mkH, err := marketing.New(deps, authSvc)
	if err != nil {
		return nil, err
	}

	// Custom org domains and unrecognized hosts fall through to the app router,
	// wrapped by marketing's custom-domain interceptor (it serves verified hosts'
	// public org pages).
	appHandler := mkH.CustomDomain(app.appRouter())
	handler := web.WithLiveReload(cfg.LiveReload, web.Dispatcher(cfg, authH.Routes(), mkH.Routes(), appHandler))
	return &Server{handler: handler, app: app, csp: csp}, nil
}

// sessionMW loads and validates the SSO session (two DB touches). It's applied to
// the route group that needs it — not to static assets or /healthz, which never
// use a session and (for static) are hit frequently. It also marks the group
// no-store: a route that can read the session can render user data, which must
// never land in a shared or history cache (see web.NoStore).
func (s *Handlers) sessionMW(r chi.Router) {
	r.Use(s.Auth.Sessions.LoadAndSave)
	r.Use(s.Auth.ValidateSession)
	r.Use(web.NoStore)
}

// withSession wraps a single handler in the session middleware, for handlers
// registered outside the session route group (e.g. the 404 handler) that still
// render page chrome and so need the session in context. It mirrors sessionMW,
// no-store included.
func (s *Handlers) withSession(h http.HandlerFunc) http.HandlerFunc {
	return s.Auth.Sessions.LoadAndSave(s.Auth.ValidateSession(web.NoStore(h))).ServeHTTP
}

// redirectToAuthLogin / redirectToAuthSignup are the app-host entry points that
// initiate the auth handoff (setting the state cookie) and bounce to the auth
// host. Cross-host "sign in"/"create account" CTAs target these, never the auth
// host directly, so the state cookie is always established first.
func (s *Handlers) redirectToAuthLogin(w http.ResponseWriter, r *http.Request) {
	s.Auth.StartLogin(w, r, r.URL.Query().Get("next"))
}

func (s *Handlers) redirectToAuthSignup(w http.ResponseWriter, r *http.Request) {
	s.Auth.StartSignup(w, r, r.URL.Query().Get("next"))
}

// appRouter is the product surface served on the app host.
func (s *Handlers) appRouter() chi.Router {
	r := chi.NewRouter()
	s.CommonMiddleware(r)
	// The app host is the authenticated product — nothing here is meant for
	// search. Blanket noindex, and tell crawlers not to crawl it at all.
	r.Use(web.NoIndex)
	r.Get("/robots.txt", web.RobotsTxt(web.RobotsDisallowAll))
	// Static assets and health don't need a session (and static is hit often), so
	// register them ahead of the session middleware, which does per-request DB work.
	s.SharedRoutes(r)
	// Branded 404 for unrouted paths. Run it through the session middleware so the
	// page chrome reflects the signed-in user (the renderer reads the session).
	r.NotFound(s.withSession(s.NotFound))

	// Everything below runs the session middleware.
	r.Group(func(r chi.Router) {
		s.sessionMW(r)

		// The handoff endpoint that turns an auth-host sign-in into an app session.
		r.Get("/session/callback", s.Auth.SessionCallback)
		// Credential UI lives on the auth host; these initiate the handoff there.
		r.Get("/login", s.redirectToAuthLogin)
		r.Get("/signup", s.redirectToAuthSignup)

		r.Get("/", s.pageHome)
		r.Get("/invite/{token}", s.pageInvite) // public: handles logged-out state

		// Authenticated area.
		r.Group(func(r chi.Router) {
			r.Use(s.Auth.RequireUser)
			r.Get("/orgs", s.pageMyOrgs)   // your organizations; discovery is on root
			r.Get("/orgs/{id}", s.pageOrg) // member view (non-members redirect to root)
			r.Get("/orgs/{id}/members", s.pageOrgMembers)
			r.Get("/orgs/{id}/repeaters", s.pageOrgRepeaters)
			r.Get("/repeaters", s.pageRepeaters)
			r.Post("/logout", s.handleLogout)
			r.Get("/repeaters/add", s.pageAddRepeater)
			r.Post("/repeaters", s.handleAddRepeater)
			r.Post("/repeaters/setup/commands", s.handleSetupCommands)
			r.Post("/repeaters/setup/complete", s.handleSetupComplete)
			r.Get("/repeaters/{id}", s.pageRepeater)
			r.Get("/repeaters/{id}/added", s.pageRepeaterAdded)
			r.Get("/repeaters/{id}/edit", s.redirectToSettings(settingsDetails))
			r.Post("/repeaters/{id}/edit", s.handleEditRepeater)
			r.Get("/repeaters/{id}/delete", s.pageDeleteRepeater)
			r.Post("/repeaters/{id}/delete", s.handleDeleteRepeater)
			r.Get("/repeaters/{id}/configure", s.pageConfigure)
			r.Get("/repeaters/{id}/console", s.redirectToConfigure(""))
			r.Get("/repeaters/{id}/console/ws", s.wsConsole)
			r.Get("/repeaters/{id}/config.json", s.consoleConfigJSON)
			r.Get("/repeaters/{id}/config", s.redirectToConfigure("configuration"))
			r.Post("/repeaters/{id}/location", s.handleSetRepeaterLocation)
			r.Get("/repeaters/{id}/log", s.redirectToConfigure("history"))
			r.Get("/repeaters/{id}/docs", s.redirectToSettings(settingsDocs))
			r.Post("/repeaters/{id}/docs", s.handleRepeaterDocs)
			r.Get("/repeaters/{id}/maintenance", s.pageRepeaterMaintenance)
			r.Post("/repeaters/{id}/maintenance", s.handleAddMaintenance)
			r.Post("/repeaters/{id}/maintenance/delete", s.handleDeleteMaintenance)
			r.Get("/repeaters/{id}/share", s.redirectToSettings(settingsPeople))
			r.Get("/repeaters/{id}/settings", s.pageRepeaterSettings)
			r.Get("/repeaters/{id}/settings/{section}", s.pageRepeaterSettings)
			r.Get("/repeaters/{id}/settings/people/new", s.pageNewInvite)
			r.Get("/repeaters/{id}/settings/people/{userID}", s.pagePersonAccess)
			r.Get("/repeaters/{id}/settings/organizations/{orgID}", s.pageRepeaterOrgLimits)
			r.Get("/repeaters/{id}/settings/ownership/transfer", s.pageTransferRepeater)
			r.Get("/repeaters/{id}/share/link/new", redirectSettingsPage(func(r *http.Request) string {
				return settingsPath(repeaterParam(r), settingsPeople) + "/new"
			}))
			r.Post("/repeaters/{id}/share/link", s.handleCreateLink)
			r.Post("/repeaters/{id}/share/link/delete", s.handleDeleteInvite)
			r.Post("/repeaters/{id}/unshare", s.handleUnshare)
			r.Get("/repeaters/{id}/transfer", redirectSettingsPage(func(r *http.Request) string {
				return settingsPath(repeaterParam(r), settingsOwnership) + "/transfer"
			}))
			r.Post("/repeaters/{id}/transfer", s.handleTransferRepeater)
			r.Get("/repeaters/{id}/share/{userID}/access", redirectSettingsPage(func(r *http.Request) string {
				return settingsPath(repeaterParam(r), settingsPeople) + "/" + chi.URLParam(r, "userID")
			}))
			r.Post("/repeaters/{id}/share/{userID}/access", s.handleSavePersonAccess)
			r.Get("/repeaters/{id}/orgs/{orgID}/limits", redirectSettingsPage(func(r *http.Request) string {
				return settingsPath(repeaterParam(r), settingsOrgs) + "/" + chi.URLParam(r, "orgID")
			}))
			r.Post("/repeaters/{id}/orgs/{orgID}/limits", s.handleSaveRepeaterOrgLimits)
			r.Post("/invite/{token}/accept", s.handleAcceptInvite)

			// Companions and check-ins.
			r.Get("/companions", s.pageCompanions)
			r.Get("/companions/new", s.pageNewCompanion)
			r.Post("/companions", s.handleAddCompanion)
			r.Get("/companions/{id}", s.pageCompanion)
			r.Post("/companions/{id}/delete", s.handleDeleteCompanion)
			r.Post("/companions/{id}/visibility", s.handleCompanionVisibility)
			r.Get("/orgs/{id}/activity", redirectOrgActivity)
			r.Get("/orgs/{id}/observers", s.redirectToObserverSettings)
			r.Post("/orgs/{id}/observers", s.handleAddOrgObserver)
			r.Post("/orgs/{id}/observers/{oid}", s.handleApproveOrgObserver)
			r.Post("/orgs/{id}/observers/{oid}/unapprove", s.handleUnapproveOrgObserver)
			r.Post("/orgs/{id}/observers/{oid}/delete", s.handleDeleteOrgObserver)
			r.Post("/orgs/{id}/broker", s.handleSaveOrgBroker)
			r.Post("/orgs/{id}/broker/delete", s.handleDeleteOrgBroker)
			r.Post("/orgs/{id}/broker/test", s.handleTestOrgBroker)
			r.Get("/orgs/{id}/nets", s.pageOrgNets)
			r.Post("/orgs/{id}/nets", s.handleCreateNet)
			r.Get("/orgs/{id}/nets/{nid}", s.pageOrgNet)
			r.Post("/orgs/{id}/nets/{nid}", s.handleUpdateNet)
			r.Post("/orgs/{id}/nets/{nid}/delete", s.handleDeleteNet)
			r.Post("/orgs/{id}/nets/{nid}/air", s.handleUpdateNetAir)
			r.Post("/orgs/{id}/nets/{nid}/ack", s.handleSetNetAck)
			r.Get("/orgs/{id}/nets/{nid}/sessions/{sid}", s.pageNetSession)
			r.Get("/orgs/{id}/nets/{nid}/sessions/{sid}/members", s.pageSessionMemberSearch)
			r.Post("/orgs/{id}/nets/{nid}/controls", s.handleAddNetControl)
			r.Post("/orgs/{id}/nets/{nid}/controls/{uid}/delete", s.handleRemoveNetControl)
			r.Post("/orgs/{id}/nets/{nid}/sessions/{sid}/acks", s.handleAcknowledge)
			r.Get("/orgs/{id}/nets/{nid}/sessions/{sid}/modem/ws", s.wsNetModem)
			r.Post("/orgs/{id}/nets/{nid}/sessions/{sid}/acks/{uid}/delete", s.handleRemoveAcknowledge)

			r.Get("/orgs/new", s.pageNewOrg)
			r.Post("/orgs", s.handleCreateOrg)
			r.Get("/orgs/{id}/settings", s.pageOrgSettings)
			r.Get("/orgs/{id}/settings/{section}", s.pageOrgSettings)
			r.Get("/orgs/{id}/settings/mqtt/observers/new", s.pageNewObserver)
			r.Get("/orgs/{id}/settings/mqtt/observers/{oid}", s.pageObserver)
			r.Get("/orgs/{id}/settings/nets/new", s.pageNewNet)
			r.Get("/orgs/{id}/settings/nets/{nid}", s.pageNetSettings)
			r.Post("/orgs/{id}/edit", s.handleEditOrg)
			r.Post("/orgs/{id}/links", s.handleSetOrgLinks)
			r.Get("/orgs/{id}/join", s.pageJoinOrg)
			r.Post("/orgs/{id}/join", s.handleJoinOrg)
			r.Post("/orgs/{id}/leave", s.handleLeaveOrg)
			r.Post("/orgs/{id}/members/{userID}", s.handleSetOrgMember)
			r.Get("/orgs/{id}/config", s.pageOrgConfig)
			r.Get("/orgs/{id}/config/profiles/new", s.pageProfileEdit)
			r.Post("/orgs/{id}/config/profiles", s.handleCreateProfile)
			r.Get("/orgs/{id}/config/profiles/{pid}/edit", s.pageProfileEdit)
			r.Post("/orgs/{id}/config/profiles/{pid}", s.handleUpdateProfile)
			r.Post("/orgs/{id}/config/profiles/{pid}/delete", s.handleDeleteProfile)
			// Regions: attributes edit in a modal (mirroring profiles), the drawn area
			// on its own workspace page. The root (*) flood policy isn't a region row,
			// so it toggles on its own.
			r.Get("/orgs/{id}/config/regions/new", s.pageRegionEdit)
			r.Post("/orgs/{id}/config/regions", s.handleCreateRegion)
			r.Get("/orgs/{id}/config/regions/{rid}/edit", s.pageRegionEdit)
			r.Post("/orgs/{id}/config/regions/{rid}", s.handleUpdateRegion)
			r.Post("/orgs/{id}/config/regions/{rid}/delete", s.handleDeleteRegion)
			r.Get("/orgs/{id}/config/regions/{rid}/area", s.pageRegionArea)
			r.Post("/orgs/{id}/config/regions/{rid}/area", s.handleSaveRegionArea)
			r.Post("/orgs/{id}/config/root-flood", s.handleSetRootFlood)
			r.Post("/orgs/{id}/config/radio", s.handleSetRadio)
			// Custom org domains are hidden for now — the hosting/TLS infrastructure
			// isn't in place yet. Leave the handlers in tree but don't expose them.
			// r.Post("/orgs/{id}/domains", s.handleAddOrgDomain)
			// r.Post("/orgs/{id}/domains/verify", s.handleVerifyOrgDomain)
			// r.Post("/orgs/{id}/domains/delete", s.handleDeleteOrgDomain)

			r.Route("/admin", func(r chi.Router) {
				r.With(s.requireCap(capAny)).Get("/", s.pageAdmin)
				r.With(s.requireCap(capAny)).Get("/analytics", s.pageAnalytics)
				r.With(s.requireCap(capAny)).Get("/proxy-test", s.pageProxyTest)
				// Build provenance. capAny, and the same facts are public at
				// web.VersionPath — the page adds only the reproduction commands,
				// so gating it higher would protect nothing.
				r.With(s.requireCap(capAny)).Get("/build", s.pageBuild)
				// CSP violation reports. Viewing is capAny, matching traffic analytics
				// — it's diagnostic data about our own pages. Clearing deletes records,
				// so it takes capUsers, the higher bar; the page hides the button
				// accordingly rather than offering one that 403s.
				r.With(s.requireCap(capAny)).Get("/csp", s.pageCSPReports)
				r.With(s.requireCap(capUsers)).Post("/csp/clear", s.handleClearCSPReports)
				r.With(s.requireCap(capCatalog)).Get("/catalog", s.pageCatalog)
				r.With(s.requireCap(capCatalog)).Post("/catalog/{id}", s.handleUpdateCommand)
				r.With(s.requireCap(capUsers)).Get("/users", s.pageUsers)
				r.With(s.requireCap(capUsers)).Get("/users/{id}/permissions", s.pageUserPermissions)
				r.With(s.requireCap(capUsers)).Get("/users/{id}/history", s.pageUserHistory)
				r.With(s.requireCap(capUsers)).Post("/users/{id}", s.handleSetUserCaps)
				// Server-identity backup/restore. Gated on capUsers — the capability that
				// already lets you grant capabilities, so the highest-trust one there is.
				// The exported value stays sealed under the master key, so exporting
				// discloses nothing to someone who lacks it (see admin_identity.go).
				r.With(s.requireCap(capUsers)).Get("/identity", s.pageIdentityBackup)
				r.With(s.requireCap(capUsers)).Post("/identity/export", s.handleExportIdentity)
				r.With(s.requireCap(capUsers)).Post("/identity/restore", s.handleRestoreIdentity)
			})
		})
	})

	return r
}

// pageHome serves the signed-in dashboard; anonymous visitors are sent to sign
// in on the auth host.
func (s *Handlers) pageHome(w http.ResponseWriter, r *http.Request) {
	if s.Auth.CurrentUserID(r.Context()) == 0 {
		s.Auth.StartLogin(w, r, "/")
		return
	}
	s.pageDashboard(w, r)
}

// pageRepeaters lists every repeater the user owns or has been shared as one
// combined list, owned first, each tagged owned/shared in the template.
func (s *Handlers) pageRepeaters(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	repeaters, err := s.Store.ListRepeatersForUser(r.Context(), uid)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	shareCounts, err := s.Store.RepeaterSharingCounts(r.Context(), uid)
	if err != nil {
		s.ServerError(w, r, "could not load sharing", err)
		return
	}
	owned, shared := splitOwnedShared(repeaters)
	combined := make([]*store.Repeater, 0, len(owned)+len(shared))
	combined = append(combined, owned...)
	combined = append(combined, shared...)
	s.Render(w, r, "repeaters.html", map[string]any{
		"Repeaters":   combined,
		"ShareCounts": shareCounts,
	})
}

// onboardingStep is one item in the dashboard getting-started checklist. Steps
// are data-driven so adding a new one (e.g. future profile fields) is a single
// entry in buildOnboarding rather than template surgery.
type onboardingStep struct {
	Title  string
	Desc   string
	Action string // CTA label, shown only while the step is pending
	Href   string // where the CTA leads (absolute when it crosses hosts)
	Done   bool
}

// dashboardOrg is one of the user's organizations on the dashboard, with the
// net that's in session or starts soonest (nil when it has none scheduled).
type dashboardOrg struct {
	store.OrgMembership
	Net *store.UserNetSummary
}

// pageDashboard renders the signed-in overview: nets in session across the
// user's organizations, a getting-started checklist while anything's pending,
// their organizations with the next net of each, their radios, and — for
// repeater owners — recent commands and a map.
func (s *Handlers) pageDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	now := time.Now()
	fail := func(what string, err error) { s.ServerError(w, r, "could not load "+what, err) }

	repeaters, err := s.Store.ListRepeatersForUser(ctx, uid)
	if err != nil {
		fail("repeaters", err)
		return
	}
	owned, shared := splitOwnedShared(repeaters)
	companions, err := s.Store.ListCompanionsForUser(ctx, uid)
	if err != nil {
		fail("companions", err)
		return
	}
	memberships, err := s.Store.ListOrgsForUser(ctx, uid)
	if err != nil {
		fail("organizations", err)
		return
	}
	nets, err := s.Store.ListUserNetSummaries(ctx, uid, now)
	if err != nil {
		fail("nets", err)
		return
	}
	recent, err := s.Store.ListRecentCommandsForOwner(ctx, uid, 8)
	if err != nil {
		fail("activity", err)
		return
	}
	user, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		fail("account", err)
		return
	}
	// Drives the passkey checklist step below. Someone who signed up with a password
	// is never asked about passkeys again after that one form, so this is the only
	// place that follows up.
	passkeys, err := s.Store.ListCredentials(ctx, uid)
	if err != nil {
		fail("passkeys", err)
		return
	}

	// Nets in session, and each org's soonest net. nets is soonest-first.
	var live []store.UserNetSummary
	soonest := map[int64]*store.UserNetSummary{}
	for i := range nets {
		n := &nets[i]
		if n.Next == nil {
			continue
		}
		if !now.Before(n.Next.StartsAt) {
			live = append(live, *n)
		}
		if _, ok := soonest[n.OrgID]; !ok {
			soonest[n.OrgID] = n
		}
	}
	orgs := make([]dashboardOrg, len(memberships))
	for i, m := range memberships {
		orgs[i] = dashboardOrg{OrgMembership: m, Net: soonest[m.Org.ID]}
	}

	unconfirmed := 0
	var mapped []*store.Repeater
	for _, rp := range owned {
		if !rp.Confirmed {
			unconfirmed++
		}
		if rp.Latitude != nil && rp.Longitude != nil {
			mapped = append(mapped, rp)
		}
	}
	unheard := 0
	for _, c := range companions {
		if !c.Confirmed() {
			unheard++
		}
	}

	// The checklist is about taking part — an organization and a companion come
	// first; running a repeater is optional, so it lives in the Your radios card
	// instead of here.
	steps := []onboardingStep{
		{
			Title:  "Join an organization",
			Desc:   "Find your local mesh group: its members, nets and recommended setup.",
			Action: "Find organizations",
			Href:   s.Origin(r, s.Cfg.RootHost) + "/orgs",
			Done:   len(memberships) > 0,
		},
		{
			Title:  "Register your companion",
			Desc:   "The MeshCore radio you carry, so your adverts count as check-ins.",
			Action: "Register companion",
			Href:   "/companions/new",
			Done:   len(companions) > 0,
		},
		{
			Title:  "Set up your profile",
			Desc:   "Add a display name so people recognize you.",
			Action: "Edit profile",
			Href:   s.Origin(r, s.Cfg.AuthHost) + "/account",
			Done:   user.DisplayName != nil && *user.DisplayName != "",
		},
		{
			// Always listed rather than shown only to password users: a passkey
			// holder sees it already satisfied, which reads as progress instead of a
			// scold, and it keeps the list identical for everyone.
			Title:  "Add a passkey",
			Desc:   "Sign in with a fingerprint, face or PIN: nothing to remember or type.",
			Action: "Add passkey",
			Href:   s.Origin(r, s.Cfg.AuthHost) + "/account",
			Done:   len(passkeys) > 0,
		},
	}
	// Only password holders are nudged, and only while mail is configured. A
	// passkey-only account genuinely gains no recovery from an address (reset sets a
	// password on accounts that have one), so asking would be asking for data we
	// can't use — the right advice for them is a second passkey, which the step
	// above already covers.
	if user.PasswordHash != nil && s.Auth.MailEnabled() {
		steps = append(steps, onboardingStep{
			Title:  "Add a recovery email",
			Desc:   "Optional, but without one a forgotten password can't be reset.",
			Action: "Add email",
			Href:   s.Origin(r, s.Cfg.AuthHost) + "/account",
			Done:   user.EmailVerified(),
		})
	}
	// People listed publicly (org admins, public repeater owners/stewards) should
	// give visitors a way to reach them. Only nudge those users, and only until
	// they set a primary contact link.
	publicRole, err := s.Store.UserHasPublicRole(ctx, uid)
	if err != nil {
		fail("account", err)
		return
	}
	if publicRole {
		links, err := s.Store.ListUserLinks(ctx, uid)
		if err != nil {
			fail("account", err)
			return
		}
		steps = append(steps, onboardingStep{
			Title:  "Add a contact link",
			Desc:   "You're listed publicly as an admin or steward; add a way for people to reach you.",
			Action: "Edit profile",
			Href:   s.Origin(r, s.Cfg.AuthHost) + "/account",
			Done:   store.PrimaryUserLink(links) != nil,
		})
	}
	doneCount := 0
	for _, st := range steps {
		if st.Done {
			doneCount++
		}
	}
	total := len(steps)
	// Hide the checklist entirely once everything's done.
	if doneCount == total {
		steps = nil
	}

	s.Render(w, r, "dashboard.html", map[string]any{
		"Now":             now,
		"LiveNets":        live,
		"Orgs":            orgs,
		"OwnedCount":      len(owned),
		"Unconfirmed":     unconfirmed,
		"SharedCount":     len(shared),
		"CompanionCount":  len(companions),
		"Unheard":         unheard,
		"Mapped":          mapped,
		"Recent":          recent,
		"Onboarding":      steps,
		"OnboardingDone":  doneCount,
		"OnboardingTotal": total,
	})
}

// splitOwnedShared partitions a repeater list into owned and directly-shared.
func splitOwnedShared(repeaters []*store.Repeater) (owned, shared []*store.Repeater) {
	for _, rp := range repeaters {
		if rp.Shared {
			shared = append(shared, rp)
		} else {
			owned = append(owned, rp)
		}
	}
	return owned, shared
}

// handleLogout signs the user out. It revokes the login row backing the session,
// which drops every host sharing it (auth SSO, root beacon, custom org domains)
// to anonymous on their next request — so no redirect chain to the auth host is
// needed. See docs/auth-cross-host.md's global logout model.
func (s *Handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context())
	s.RedirectAfterLogout(w, r)
}
