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

//go:embed templates/kpi_partials.html
var kpiPartialsHTML string

//go:embed templates/kpi_overview.html
var kpiOverviewHTML string

//go:embed templates/kpi_prs.html
var kpiPRsHTML string

//go:embed templates/kpi_failure.html
var kpiFailureHTML string

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

//go:embed assets/kpi.js
var kpiJS string

// Build-time embedded assets; trusted, so safe to mark as CSS/JS.
var (
	styleAsset = template.CSS(styleCSS) //nolint:gosec // G203: embedded at build time
	themeAsset = template.JS(themeJS)   //nolint:gosec // G203: embedded at build time
	homeAsset  = template.JS(homeJS)    //nolint:gosec // G203: embedded at build time
	repoAsset  = template.JS(repoJS)    //nolint:gosec // G203: embedded at build time
	runAsset   = template.JS(runJS)     //nolint:gosec // G203: embedded at build time
	kpiAsset   = template.JS(kpiJS)     //nolint:gosec // G203: embedded at build time
)

// queryURL returns "?"+encoded query; html/template normalizes it in href.
func queryURL(v url.Values) string {
	return "?" + v.Encode()
}

var pageTmpl = template.Must(template.New("layout").Funcs(template.FuncMap{
	"shortSHA":   shortSHA,
	"githubRepo": githubRepo,
	"add":        func(a, b int) int { return a + b },
	"sub":        func(a, b int) int { return a - b },
	"div": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a / b
	},
	"kpiQuery": kpiPageQuery,
	"homeQuery": func(page int, repository, branchFilter string) string {
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
	"repoQuery": func(page int, _, branchFilter string) string {
		v := url.Values{}
		if page > 1 {
			v.Set("page", strconv.Itoa(page))
		}
		if branchFilter != "" {
			v.Set("branch_filter", branchFilter)
		}
		return queryURL(v)
	},
	"runQuery": func(page int, ghRunID, ghRunAttempt string) string {
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
}).Parse(layoutHTML + homeHTML + repoHTML + runHTML + kpiPartialsHTML + kpiOverviewHTML + kpiPRsHTML + kpiFailureHTML))

type layoutData struct {
	Site           SiteInfo
	IsKPI          bool
	ShowLiveStatus bool
	Home           *HomePage
	Repo           *RepoPage
	Run            *RunPage
	KPIOverview    *KPIOverviewPage
	KPIPRs         *KPIPRsPage
	KPIFailure     *KPIFailureDetailPage
	CSS            template.CSS
	ThemeJS        template.JS
	PageJS         template.JS
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
		Site:           page.Site,
		ShowLiveStatus: true,
		Home:           &page,
		CSS:            styleAsset,
		ThemeJS:        themeAsset,
		PageJS:         homeAsset,
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
		Site:           page.Site,
		ShowLiveStatus: true,
		Repo:           &page,
		CSS:            styleAsset,
		ThemeJS:        themeAsset,
		PageJS:         repoAsset,
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
		Site:           page.Site,
		ShowLiveStatus: true,
		Run:            &page,
		CSS:            styleAsset,
		ThemeJS:        themeAsset,
		PageJS:         runAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderKPIOverview renders the KPI overview page HTML.
func RenderKPIOverview(page KPIOverviewPage) ([]byte, error) {
	data := layoutData{
		Site:        page.Site,
		IsKPI:       true,
		KPIOverview: &page,
		CSS:         styleAsset,
		ThemeJS:     themeAsset,
		PageJS:      kpiAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderKPIPRs renders the KPI PR E2E reliability page HTML.
func RenderKPIPRs(page KPIPRsPage) ([]byte, error) {
	data := layoutData{
		Site:    page.Site,
		IsKPI:   true,
		KPIPRs:  &page,
		CSS:     styleAsset,
		ThemeJS: themeAsset,
		PageJS:  kpiAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderKPIFailureDetail renders the KPI failure signature detail page HTML.
func RenderKPIFailureDetail(page KPIFailureDetailPage) ([]byte, error) {
	data := layoutData{
		Site:       page.Site,
		IsKPI:      true,
		KPIFailure: &page,
		CSS:        styleAsset,
		ThemeJS:    themeAsset,
		PageJS:     kpiAsset,
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
