package triagework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Config contains deploy-time Jira connection settings.
type Config struct {
	Enabled    bool
	BaseURL    string
	Email      string
	APIToken   string
	ProjectKey string
	IssueType  string
	HTTPClient *http.Client
}

// JiraClient implements the Jira Cloud REST v3 integration.
type JiraClient struct {
	baseURL   string
	email     string
	token     string
	project   string
	issueType string
	client    *http.Client
}

var jiraProjectPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,49}$`)
var jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)
var jiraLabelPattern = regexp.MustCompile(`^tsio-(test-[a-f0-9]{64}|submission-[a-f0-9-]{36})$`)

// NewJiraClient fails startup when enabled but incomplete. URLs are configured
// here, never accepted from a repair request. Redirects cannot forward secrets.
func NewJiraClient(c Config) (*JiraClient, error) {
	if !c.Enabled {
		return nil, nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("enabled Jira requires an HTTPS origin without path, credentials, query or fragment")
	}
	if !bounded(c.Email, 320) || !bounded(c.APIToken, 4096) || !jiraProjectPattern.MatchString(c.ProjectKey) || !bounded(c.IssueType, 100) {
		return nil, errors.New("enabled Jira requires email, API token, project key and issue type")
	}
	client := http.Client{Timeout: 15 * time.Second}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
		if client.Timeout <= 0 || client.Timeout > 20*time.Second {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &JiraClient{baseURL: strings.TrimSuffix(c.BaseURL, "/"), email: c.Email, token: c.APIToken, project: c.ProjectKey, issueType: c.IssueType, client: &client}, nil
}

func (j *JiraClient) call(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, j.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.SetBasicAuth(j.email, j.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return errors.New("Jira request failed or timed out")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Jira returned HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("Jira returned an invalid response")
	}
	return nil
}

// Jira Cloud's enhanced search and ADF create schema:
// https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/
// https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/
func (j *JiraClient) find(ctx context.Context, label string, unresolved bool) (*Issue, error) {
	if !jiraLabelPattern.MatchString(label) {
		return nil, errors.New("invalid Jira dedup label")
	}
	jql := `project = "` + j.project + `" AND labels = "` + label + `"`
	if unresolved {
		jql += ` AND resolution IS EMPTY`
	}
	body := map[string]any{"jql": jql, "maxResults": 2, "fields": []string{"key", "resolution"}}
	var response struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Resolution json.RawMessage `json:"resolution"`
			} `json:"fields"`
		} `json:"issues"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := j.call(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &response); err != nil {
		return nil, err
	}
	if len(response.Issues) > 1 || response.NextPageToken != "" {
		return nil, errors.New("Jira dedup returned multiple matches; manual reconciliation required")
	}
	if len(response.Issues) == 0 {
		return nil, nil
	}
	result := response.Issues[0]
	if unresolved && string(result.Fields.Resolution) != "null" {
		return nil, errors.New("Jira unresolved search returned missing or resolved state")
	}
	return j.issue(result.Key)
}
func (j *JiraClient) issue(key string) (*Issue, error) {
	if !jiraKeyPattern.MatchString(key) {
		return nil, errors.New("Jira returned an invalid issue key")
	}
	return &Issue{Key: key, URL: j.baseURL + "/browse/" + key}, nil
}

// FindUnresolved searches current unresolved issues by logical test label.
func (j *JiraClient) FindUnresolved(ctx context.Context, label string) (*Issue, error) {
	return j.find(ctx, label, true)
}

// FindSubmission finds a durable submission marker including closed issues.
func (j *JiraClient) FindSubmission(ctx context.Context, label string) (*Issue, error) {
	return j.find(ctx, label, false)
}

// IsUnresolved fetches live resolution directly to avoid search index lag.
func (j *JiraClient) IsUnresolved(ctx context.Context, key string) (bool, error) {
	if !jiraKeyPattern.MatchString(key) {
		return false, errors.New("invalid Jira issue key")
	}
	var out struct {
		Fields struct {
			Resolution json.RawMessage `json:"resolution"`
		} `json:"fields"`
	}
	if err := j.call(ctx, http.MethodGet, "/rest/api/3/issue/"+key+"?fields=resolution", nil, &out); err != nil {
		return false, err
	}
	if len(out.Fields.Resolution) == 0 {
		return false, errors.New("Jira omitted issue resolution")
	}
	return string(out.Fields.Resolution) == "null", nil
}

// Create submits one issue with both logical-test and submission markers.
func (j *JiraClient) Create(ctx context.Context, testLabel, submissionLabel, summary, description string) (*Issue, error) {
	if !jiraLabelPattern.MatchString(testLabel) || !jiraLabelPattern.MatchString(submissionLabel) {
		return nil, errors.New("invalid Jira label")
	}
	doc := map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": description}}}}}
	body := map[string]any{"fields": map[string]any{"project": map[string]string{"key": j.project}, "issuetype": map[string]string{"name": j.issueType}, "summary": summary, "description": doc, "labels": []string{testLabel, submissionLabel}}}
	var out struct {
		Key string `json:"key"`
	}
	if err := j.call(ctx, http.MethodPost, "/rest/api/3/issue", body, &out); err != nil {
		return nil, err
	}
	return j.issue(out.Key)
}
