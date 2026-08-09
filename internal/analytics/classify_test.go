package analytics

import "testing"

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)"

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		status int
		ua     string
		want   string
	}{
		// Real traffic.
		{"page view", "/orgs/example-mesh", 200, browserUA, KindVisit},
		{"redirect", "/", 301, browserUA, KindVisit},
		{"form post", "/orgs/new", 302, browserUA, KindVisit},
		{"server error is still a visit", "/dashboard", 500, browserUA, KindVisit},
		{"forbidden is still a visit", "/admin/users", 403, browserUA, KindVisit},

		// Scanner traffic, all pulled from production logs. Every one arrived with
		// a browser user agent, which is why the UA check alone never caught them.
		{"php config", "/wp_mail_smtp.ini", 404, browserUA, KindProbe},
		{"php index", "/index.php", 404, browserUA, KindProbe},
		{"phpinfo", "/includes/phpinfo.php", 404, browserUA, KindProbe},
		{"firebase key", "/firebase-key.json", 404, browserUA, KindProbe},
		{"cli config", "/.vultr-cli.yaml", 404, browserUA, KindProbe},
		{"tomcat manager", "/manager/html", 404, browserUA, KindProbe},
		{"dotenv", "/.env", 404, browserUA, KindProbe},
		{"git config", "/.git/config", 404, browserUA, KindProbe},
		{"wordpress login", "/wp-login.php", 404, browserUA, KindProbe},
		{"uppercase replay", "/WP-ADMIN/SETUP-CONFIG.PHP", 404, browserUA, KindProbe},

		// Missed by the first version of this list — the reason it was rewritten.
		{"laravel ignition rce", "/index.php/_ignition/execute-solution", 404, browserUA, KindProbe},
		{"graphql", "/graphql", 404, browserUA, KindProbe},
		{"graphql under api", "/api/graphql", 404, browserUA, KindProbe},
		{"apache status", "/server-status", 404, browserUA, KindProbe},
		{"docker registry", "/v2/_catalog", 404, browserUA, KindProbe},
		{"vite env", "/@vite/env", 404, browserUA, KindProbe},
		{"aspnet trace", "/trace.axd", 404, browserUA, KindProbe},
		{"struts action", "/login.action", 404, browserUA, KindProbe},
		{"cpanel proxy", "/___proxy_subdomain_cpanel", 404, browserUA, KindProbe},
		{"symfony profiler", "/_profiler/phpinfo", 404, browserUA, KindProbe},
		{"ds_store", "/.DS_Store", 404, browserUA, KindProbe},
		{"stripe dotfile", "/.stripe/", 404, browserUA, KindProbe},
		{"bare api", "/api", 404, browserUA, KindProbe},
		{"bare console", "/console/", 404, browserUA, KindProbe},
		{"root config.json", "/config.json", 404, browserUA, KindProbe},
		{"unfilled wildcard", "/workspaces/*", 404, browserUA, KindProbe},
		{"unfilled wildcard 2", "/webhook-waiting/*", 404, browserUA, KindProbe},

		// .env variants — an exact-name list can't keep up, the dotfile rule can.
		{"env tilde", "/.env~", 404, browserUA, KindProbe},
		{"env copy", "/.env_copy", 404, browserUA, KindProbe},
		{"env backup2", "/.env.backup2", 404, browserUA, KindProbe},
		{"nested env", "/backend/.env", 404, browserUA, KindProbe},

		// A scanner sweeping the www host gets a 301, not a 404. Gating probes on
		// 404 alone filed all of this as ordinary traffic.
		{"probe redirected by www host", "/.env", 301, browserUA, KindProbe},
		{"probe on a 500", "/wp-login.php", 500, browserUA, KindProbe},

		// Bots.
		{"googlebot", "/orgs", 200, "Googlebot/2.1 (+http://www.google.com/bot.html)", KindBot},
		{"empty ua", "/orgs", 200, "", KindBot},
		{"uptime monitor", "/", 200, "Better Uptime Monitor", KindBot},

		// A scanner that also calls itself a bot is still a scanner.
		{"bot ua on a probe path", "/wp-login.php", 404, "evilbot/1.0", KindProbe},

		// Honest 404s stay visible as their own kind rather than being written off
		// as attacks — a broken link is something worth fixing.
		{"stale link", "/orgs/closed-club", 404, browserUA, KindNotFound},
		{"typo", "/repeaterz", 404, browserUA, KindNotFound},
		{"crawler on a dead link", "/orgs/gone", 404, "Googlebot/2.1", KindBot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.path, c.status, c.ua); got != c.want {
				t.Errorf("classify(%q, %d, %q) = %q, want %q", c.path, c.status, c.ua, got, c.want)
			}
		})
	}
}

// TestClassifyProductionCorpus runs the classifier over paths taken verbatim
// from production traffic. The same corpus was checked against migration 0045's
// SQL backfill, which found zero disagreements — keeping this list here is what
// stops the Go rules and the SQL rules drifting apart unnoticed.
func TestClassifyProductionCorpus(t *testing.T) {
	probes := []string{
		"/xmlrpc.php", "/info.php", "/test.php", "/.aws/credentials", "/actuator/env",
		"/.vscode/sftp.json", "/api/gql", "/graphql/api", "/debug/default/view",
		"/___proxy_subdomain_whm/login", "/stats/prometheus", "/.linode-cli",
		"/config.js", "/aws.config.js", "/info", "/env", "/server", "/phpinfo",
		"/.env.save", "/.env.bak", "/.env.prod", "/.env.dev", "/.env.old", "/.env.example",
		"/app/.env", "/api/.env", "/backend/.env",
		"/ecp/Current/exporttool/microsoft.exchange.ediscovery.exporttool.application",
		// Backup/editor suffixes appended AFTER a real extension.
		"/phpinfo.php~", "/phpinfo.php.save", "/config.json.save",
		// Credential JSON under other stacks' names — no fixed list keeps up,
		// which is why .json is scoped by prefix instead.
		"/gcp-credentials.json", "/google-credentials.json", "/firebase-adminsdk.json",
		"/aws-ses.json", "/aws.json", "/env.json", "/config/production.json",
		"/appsettings.Development.json", "/appsettings.Production.json",
		// Loose source and infra files.
		"/aws-config.js", "/app.js", "/index.js", "/server.js", "/env.js", "/js/config.js",
		"/settings.py", "/terraform.tfstate", "/Dockerfile", "/_environment", "/old/",
		"/s/230313e28343e2333313e28363/_/;/META-INF/maven/com.atlassian.jira/jira-webapp-dist/pom.properties",
	}
	for _, p := range probes {
		if got := classify(p, 404, browserUA); got != KindProbe {
			t.Errorf("classify(%q, 404) = %q, want %q", p, got, KindProbe)
		}
	}

	// Seen in production 404s and NOT attacks: these must stay visible as broken
	// links so they can be fixed.
	// Real people looking for pages we don't have, and crawlers on the auth host.
	// These must stay readable as gaps to fill, not get written off as attacks.
	notFound := []string{
		"/about", "/contact", "/contact-us", "/login",
		"/login/sitemap.xml", "/login/robots.txt",
		"/orgs/example-mesh/config/edit", "/repeaters/Nf5YgD1sJw6k/console",
	}
	for _, p := range notFound {
		if got := classify(p, 404, browserUA); got != KindNotFound {
			t.Errorf("classify(%q, 404) = %q, want %q", p, got, KindNotFound)
		}
	}
}

// TestOwnRoutesAreNotProbes guards the signature list against our own URL space.
// The rules match extensions, filenames, and dotfiles, and MeshTender genuinely
// serves .json endpoints, has "config" all through the org routes, and has a
// /console under each repeater — a loose rule here would file a member's stale
// bookmark under "attacker".
func TestOwnRoutesAreNotProbes(t *testing.T) {
	ours := []string{
		"/orgs/example-mesh/repeaters.json",
		"/repeaters/Ab3xKp9QmR2t/config.json",
		"/orgs/example-mesh/config",
		"/orgs/example-mesh/config/profiles/new",
		"/orgs/example-mesh/config/regions/12/area",
		"/orgs/example-mesh/config/root-flood",
		"/orgs/example-mesh/my-commands",
		"/repeaters/Ab3xKp9QmR2t/console",
		"/repeaters/Zq7WnT4vLh8c/console",
		"/account/passkeys/rename",
		"/api/login/discoverable/begin",
		"/catalog/heltec-v3",
		"/invite/:token",
		"/build",
	}
	for _, p := range ours {
		if isProbePath(p) {
			t.Errorf("isProbePath(%q) = true, but that's one of our own routes", p)
		}
		if got := classify(p, 404, browserUA); got != KindNotFound {
			t.Errorf("classify(%q, 404) = %q, want %q — a 404 on our own route is a broken link, not an attack", p, got, KindNotFound)
		}
	}
}

// TestConventionalPathsAreNotProbes: these 404 in production today and are worth
// serving, not worth calling attacks. Filing them under "probe" would bury the
// signal that we ought to add them — real iOS devices and search engines are
// asking. .well-known in particular has to survive the dotfile rule.
func TestConventionalPathsAreNotProbes(t *testing.T) {
	conventional := []string{
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/favicon.png",
		"/sitemap.xml",
		"/robots.txt",
		"/llms.txt",
		"/.well-known/security.txt",
		"/.well-known/change-password",
	}
	for _, p := range conventional {
		if isProbePath(p) {
			t.Errorf("isProbePath(%q) = true, but that's a web convention worth serving", p)
		}
	}
}
