package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// credentialLikeRe matches the input attributes password managers take as a
// login: a password or email field, or one named or autocompleted as a
// username or email.
var credentialLikeRe = regexp.MustCompile(`(?i)\btype="(password|email)"|\bname="(username|email)"|\bautocomplete="[^"]*\b(username|email|current-password)\b`)

// linkValueRe matches a link editor's value field (link-editor.js). Its
// placeholder is set per platform at run time ("@username", "username or
// invite link", "you@example.com"), and 1Password reads placeholders, so it
// took a contact link for a login's username field on the account and org
// settings pages. The markup can't show that, so the class is the rule.
var linkValueRe = regexp.MustCompile(`(?i)\bclass="[^"]*\blink-value\b`)

// signInTemplates are the auth host's sign-in flows, where every
// credential-looking field really is the user's MeshTender login.
var signInTemplates = map[string]bool{"login.html": true, "signup.html": true, "forgot.html": true, "reset.html": true}

// ownPasswordRe matches the user's own MeshTender password, being checked or
// changed (the account and delete-account pages).
var ownPasswordRe = regexp.MustCompile(`(?i)\btype="password"[^>]*\bautocomplete="(current|new)-password"|\bautocomplete="(current|new)-password"[^>]*\btype="password"`)

// TestNonLoginCredentialFieldsAreIgnoredByPasswordManagers: outside the
// sign-in pages, a field that looks like a credential is either someone
// else's (an account's recovery address, an MQTT broker's login, a repeater's
// password, a channel key) or the user's own being edited (their username or
// email on the account page). 1Password reads a lone username or email field
// in a form as a login screen and offers to sign in or save one, ignoring
// autocomplete="off", so each must carry data-1p-ignore. The user's own
// password fields on the auth host keep prompting: 1Password recognizes a
// password check or change, and should.
func TestNonLoginCredentialFieldsAreIgnoredByPasswordManagers(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	authTemplates := filepath.Join(root, "internal", "auth", "templates") + string(filepath.Separator)
	inputRe := regexp.MustCompile(`(?is)<input\b([^>]*)>`)
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		auth := strings.HasPrefix(path, authTemplates)
		if auth && signInTemplates[filepath.Base(path)] {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // walking our own templates
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, loc := range inputRe.FindAllSubmatchIndex(b, -1) {
			attrs := b[loc[2]:loc[3]]
			if auth && ownPasswordRe.Match(attrs) {
				continue
			}
			if (credentialLikeRe.Match(attrs) || linkValueRe.Match(attrs)) && !strings.Contains(string(attrs), "data-1p-ignore") {
				t.Errorf("%s: credential-like <input> without data-1p-ignore: %q", rel, snippet(b, loc[0]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}
