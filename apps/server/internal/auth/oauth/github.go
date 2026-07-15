// Package oauth implements the GitHub OAuth authorization-code flow used for
// human sign-in.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	oauthgh "golang.org/x/oauth2/github"
)

// httpTimeout bounds the token exchange and user-profile calls to GitHub so a
// hung upstream can never block an OAuth callback indefinitely.
const httpTimeout = 15 * time.Second

// Config bundles what the GitHub OAuth flow needs.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// GitHubUser is what we consume from GET /user after OAuth.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// Flow is the runtime pairing of config + http client.
type Flow struct {
	cfg      Config
	oaConfig *oauth2.Config
	client   *http.Client
}

// NewFlow builds a Flow. Scopes default to read:user + user:email if nil.
func NewFlow(cfg Config) *Flow {
	scopes := cfg.Scopes
	if scopes == nil {
		scopes = []string{"read:user", "user:email"}
	}
	return &Flow{
		cfg: cfg,
		oaConfig: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
			Endpoint:     oauthgh.Endpoint,
		},
		client: &http.Client{Timeout: httpTimeout},
	}
}

// Start generates the authorize URL and the opaque state parameter. The state
// MUST be stashed by the caller (typically in a cookie) and compared in Callback.
func (f *Flow) Start() (authorizeURL, state string, err error) {
	s, err := randomState()
	if err != nil {
		return "", "", err
	}
	return f.oaConfig.AuthCodeURL(s, oauth2.AccessTypeOffline), s, nil
}

// Exchange swaps the authorization code for a token and fetches the user profile.
func (f *Flow) Exchange(ctx context.Context, code string) (*oauth2.Token, *GitHubUser, error) {
	// Drive the token exchange through the timeout-bounded client too.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, f.client)
	tok, err := f.oaConfig.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("oauth exchange: %w", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch github user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("github user api status %d", resp.StatusCode)
	}
	var u GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, nil, fmt.Errorf("decode user: %w", err)
	}
	if u.ID == 0 {
		return nil, nil, errors.New("github user missing id")
	}
	return tok, &u, nil
}

func randomState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
