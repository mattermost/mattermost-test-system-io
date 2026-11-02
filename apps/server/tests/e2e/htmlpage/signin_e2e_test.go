//go:build e2e
// +build e2e

package htmlpagee2e

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oauth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

var signInHrefRE = regexp.MustCompile(`<a href="([^"]+)" class="btn-signin">`)

// Following the rendered Sign in link starts GitHub OAuth: a redirect plus the state cookie.
func TestSignInLinkStartsGitHubOAuth(t *testing.T) {
	env := testenv.Start(t, testenv.WithHTMLView(), testenv.WithOAuth(oauth.NewFlow(oauth.Config{
		ClientID:     "e2e-client",
		ClientSecret: "e2e-secret",
		RedirectURL:  "http://localhost/api/v1/auth/github/callback",
	})))

	resp, err := http.Get(env.ServerURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := signInHrefRE.FindSubmatch(page)
	if m == nil {
		t.Fatalf("home page has no Sign in link")
	}
	href := string(m[1])
	if href != "/api/v1/auth/github" {
		t.Fatalf("Sign in href = %q, want /api/v1/auth/github", href)
	}

	// Follow the link the way a browser would, without auto-following the redirect.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = client.Get(env.ServerURL + href)
	if err != nil {
		t.Fatalf("GET %s: %v", href, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET %s = %d, want 302", href, resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), "https://github.com/login/oauth/authorize") {
		t.Fatalf("Location = %q, want GitHub authorize URL", resp.Header.Get("Location"))
	}
	state := loc.Query().Get("state")
	if state == "" || loc.Query().Get("client_id") != "e2e-client" {
		t.Fatalf("authorize query = %v, want client_id and state", loc.Query())
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "tsio_oauth_state" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("no tsio_oauth_state cookie in %v", resp.Header.Values("Set-Cookie"))
	}
	if !cookie.HttpOnly {
		t.Errorf("state cookie is not HttpOnly")
	}
}
