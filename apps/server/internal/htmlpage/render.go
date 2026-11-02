package htmlpage

import (
	"bytes"
	"html/template"
	"net/url"
	"strconv"

	_ "embed"
)

//go:embed templates/layout.html
var layoutHTML string

//go:embed templates/home.html
var homeHTML string

//go:embed templates/repo.html
var repoHTML string

//go:embed templates/run.html
var runHTML string

//go:embed assets/style.css
var styleCSS string

//go:embed assets/theme.js
var themeJS string

//go:embed assets/home.js
var homeJS string

//go:embed assets/repo.js
var repoJS string

//go:embed assets/run.js
var runJS string

// Build-time embedded assets; trusted, so safe to mark as CSS/JS.
var (
	styleAsset = template.CSS(styleCSS) //nolint:gosec // G203: embedded at build time
	themeAsset = template.JS(themeJS)   //nolint:gosec // G203: embedded at build time
	homeAsset  = template.JS(homeJS)    //nolint:gosec // G203: embedded at build time
	repoAsset  = template.JS(repoJS)    //nolint:gosec // G203: embedded at build time
	runAsset   = template.JS(runJS)     //nolint:gosec // G203: embedded at build time
)

// queryURL returns "?"+encoded query; url.Values.Encode escapes all values.
func queryURL(v url.Values) template.URL {
	return template.URL("?" + v.Encode()) //nolint:gosec // G203: encoder-escaped query
}

var pageTmpl = template.Must(template.New("layout").Funcs(template.FuncMap{
	"shortSHA":   shortSHA,
	"githubRepo": githubRepo,
	"add":        func(a, b int) int { return a + b },
	"sub":        func(a, b int) int { return a - b },
	"homeQuery": func(page int, repository, branchFilter string) template.URL {
		v := url.Values{}
		if page > 1 {
			v.Set("page", strconv.Itoa(page))
		}
		if repository != "" {
			v.Set("repository", repository)
		}
		if branchFilter != "" {
			v.Set("branch_filter", branchFilter)
		}
		return queryURL(v)
	},
	"repoQuery": func(page int, _, branchFilter string) template.URL {
		v := url.Values{}
		if page > 1 {
			v.Set("page", strconv.Itoa(page))
		}
		if branchFilter != "" {
			v.Set("branch_filter", branchFilter)
		}
		return queryURL(v)
	},
	"runQuery": func(page int, ghRunID, ghRunAttempt string) template.URL {
		v := url.Values{}
		if ghRunID != "" {
			v.Set("gh_run_id", ghRunID)
		}
		if ghRunAttempt != "" && ghRunAttempt != "1" {
			v.Set("gh_run_attempt", ghRunAttempt)
		}
		if page > 1 {
			v.Set("page", strconv.Itoa(page))
		}
		return queryURL(v)
	},
}).Parse(layoutHTML + homeHTML + repoHTML + runHTML))

type layoutData struct {
	Site    SiteInfo
	Home    *HomePage
	Repo    *RepoPage
	Run     *RunPage
	CSS     template.CSS
	ThemeJS template.JS
	PageJS  template.JS
}

// RenderHomeLiveRows renders the live rows HTML fragment.
func RenderHomeLiveRows(runs []HomeRunRow) ([]byte, error) {
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "home-live-rows", runs); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderRunDispatchLive renders header chips + unit rows for the live fragment.
func RenderRunDispatchLive(live RunDispatchLive) ([]byte, error) {
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "run-dispatch-body", live); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderHome renders the home page HTML.
func RenderHome(page HomePage) ([]byte, error) {
	data := layoutData{
		Site:    page.Site,
		Home:    &page,
		CSS:     styleAsset,
		ThemeJS: themeAsset,
		PageJS:  homeAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderRepo renders the repo page HTML.
func RenderRepo(page RepoPage) ([]byte, error) {
	data := layoutData{
		Site:    page.Site,
		Repo:    &page,
		CSS:     styleAsset,
		ThemeJS: themeAsset,
		PageJS:  repoAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderRun renders the consolidated run page.
func RenderRun(page RunPage) ([]byte, error) {
	data := layoutData{
		Site:    page.Site,
		Run:     &page,
		CSS:     styleAsset,
		ThemeJS: themeAsset,
		PageJS:  runAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
