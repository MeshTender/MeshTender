package analytics

import "strings"

// Event kinds. Every recorded request gets exactly one, decided at record time
// by classify. They're stored on the row and on every rollup so the dashboard
// can read one kind at a time — scanner noise is kept, not discarded, but it
// never lands in the same bucket as a person visiting a page.
const (
	KindVisit    = "visit"    // a real request that hit a real route
	KindProbe    = "probe"    // a request for something only an attacker asks for
	KindNotFound = "notfound" // a 404 with no attack signature — likely a broken link
	KindBot      = "bot"      // a self-identified crawler or monitor
)

// probeExtensions are file types we serve nowhere. Matched against every path
// SEGMENT, not just the last one: the Laravel Ignition RCE arrives as
// /index.php/_ignition/execute-solution, where the ".php" sits mid-path.
var probeExtensions = []string{
	".php", ".phps", ".php3", ".php5", ".php7",
	".ini", ".env", ".yaml", ".yml", ".sql", ".py", ".tfstate", ".properties",
	".asp", ".aspx", ".axd", ".cgi", ".jsp", ".jspx", ".action",
	// .js is safe to claim: the only JavaScript we serve lives under /static/,
	// which skip() drops before anything is recorded, so a .js that reaches the
	// classifier is by definition not ours.
	".js",
	// Editor and backup droppings. These also arrive appended to a real
	// extension (/phpinfo.php.save, /config.json.save), which is why the check
	// below strips them and re-tests rather than only matching the tail.
	".bak", ".old", ".swp", ".save", ".orig", ".copy", ".dist", ".tmp",
}

// backupSuffixes get stripped from a segment before the extension check runs, so
// /phpinfo.php~ is recognized as the .php probe it is.
var backupSuffixes = []string{"~", ".save", ".bak", ".old", ".orig", ".copy", ".dist", ".tmp", ".backup"}

// probeNames match a path segment exactly — credential and config files that
// ship with other stacks. Matching the whole segment (never a substring) is what
// keeps our own /orgs/{id}/repeaters.json and /repeaters/{id}/config.json out of
// this bucket; note that bare "config.json" is deliberately absent for exactly
// that reason and is handled by rootProbes instead.
var probeNames = []string{
	"firebase-key.json", "credentials.json", "service-account.json",
	"secrets.json", "settings.json", "appsettings.json", "sftp.json",
	"package.json", "composer.json", "web.config",
	"id_rsa", "id_dsa", "backup.zip", "backup.tar.gz", "dockerfile",
}

// jsonRoots are the only path prefixes under which we serve JSON
// (/orgs/{id}/repeaters.json and /repeaters/{id}/config.json). A .json anywhere
// else is someone fishing for another stack's credentials — production alone
// turned up gcp-credentials.json, firebase-adminsdk.json, aws-ses.json and
// appsettings.Production.json, which no fixed list of names would have kept up
// with. Scoping by prefix rather than by name keeps a 404 on our own two
// endpoints readable as the broken link it is.
var jsonRoots = []string{"/orgs/", "/repeaters/"}

// rootProbes are generic names that are only suspicious at the root of a host.
// "/api" and "/console" are scanner bait; /api/login/begin and
// /repeaters/{id}/console are ours. Exact full-path matches only.
var rootProbes = []string{
	"/api", "/info", "/env", "/server", "/phpinfo", "/console", "/console/",
	"/config.json", "/config.js", "/aws.config.js",
	"/server-status", "/server-info", "/v2/_catalog", "/old/",
}

// probeSegments are fragments from the standard scanner wordlists — other
// stacks' admin panels, framework internals, and known RCE entry points. Each is
// specific enough not to collide with our own URL space.
var probeSegments = []string{
	"wp-", "wordpress", "xmlrpc", "phpmyadmin", "/pma/", "adminer", "cgi-bin",
	"/vendor/", "autodiscover", "/owa/", "/ecp/", "manager/html", "/solr/",
	"jenkins", "actuator", "telescope", "eval-stdin", "hnap1",
	"graphql", "/gql", "_profiler", "@vite", "___proxy_subdomain",
	"debug/default", "_catalog", "_ignition", "webhook-waiting",
	"stats/prometheus", "/goform/", "/boaform/", "_environment", "meta-inf",
}

// classify buckets one finished request.
//
// A probe is a request for something we don't serve, that only an attacker asks
// for. The status gate is "not a 2xx": a scanner sweeping the www host gets a
// 301 to the apex rather than a 404, and gating on 404 alone let all of that
// through as ordinary traffic. A 2xx means we really do serve the path, so the
// signature must be wrong and the request stays a visit — that direction is the
// safe one to be wrong in.
//
// The signatures only decide which flavor of non-2xx it was, so a miss can never
// hide a real request: it degrades to "notfound", still visible, just not
// attributed to an attacker. Bots are checked after probes because a scanner is
// free to put "bot" in its user agent, and what it asked for is better evidence
// than what it calls itself.
//
// The SQL backfill in migration 0045 mirrors these rules; it runs once over
// history and the two aren't kept in lockstep afterwards.
func classify(path string, status int, ua string) string {
	if !isSuccess(status) && isProbePath(path) {
		return KindProbe
	}
	if isBot(ua) {
		return KindBot
	}
	if status == 404 {
		return KindNotFound
	}
	return KindVisit
}

// isSuccess reports whether the response actually served the path.
func isSuccess(status int) bool { return status >= 200 && status < 300 }

// isProbePath reports whether a path looks like it came off a scanner wordlist.
// Case-insensitive: the same list gets replayed in every casing.
func isProbePath(path string) bool {
	p := strings.ToLower(path)

	// A literal "*" is an unfilled placeholder from the scanner's own template
	// (/workspaces/*, /webhook-waiting/*). No browser ever sends one.
	if strings.Contains(p, "*") {
		return true
	}
	for _, s := range rootProbes {
		if p == s {
			return true
		}
	}
	for _, s := range probeSegments {
		if strings.Contains(p, s) {
			return true
		}
	}
	jsonIsOurs := false
	for _, root := range jsonRoots {
		if strings.HasPrefix(p, root) {
			jsonIsOurs = true
			break
		}
	}

	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			continue
		}
		// We serve no dotfiles. .well-known is the one real convention, and
		// exempting it keeps security.txt and friends out of the attack bucket.
		if seg[0] == '.' && seg != ".well-known" {
			return true
		}
		if strings.HasSuffix(seg, ".json") && !jsonIsOurs {
			return true
		}
		for _, s := range probeNames {
			if seg == s {
				return true
			}
		}
		// Strip editor/backup droppings before testing the extension, so
		// /phpinfo.php.save and /phpinfo.php~ read as the .php probes they are.
		base := seg
		for changed := true; changed; {
			changed = false
			for _, s := range backupSuffixes {
				if trimmed, ok := strings.CutSuffix(base, s); ok && trimmed != "" {
					base, changed = trimmed, true
				}
			}
		}
		for _, s := range probeExtensions {
			if strings.HasSuffix(seg, s) || strings.HasSuffix(base, s) {
				return true
			}
		}
	}
	return false
}

// isBot reports whether the user agent identifies itself as automated. An empty
// user agent counts: every real browser sends one.
func isBot(ua string) bool {
	ua = strings.ToLower(ua)
	if ua == "" {
		return true
	}
	for _, s := range []string{"bot", "crawl", "spider", "slurp", "headless", "preview", "monitor"} {
		if strings.Contains(ua, s) {
			return true
		}
	}
	return false
}
