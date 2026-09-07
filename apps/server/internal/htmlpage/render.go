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

//go:embed assets/style.css
var styleCSS string

//go:embed assets/theme.js
var themeJS string

//go:embed assets/home.js
var homeJS string

//go:embed assets/repo.js
var repoJS string

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
		q := v.Encode()
		if q == "" {
			return template.URL("?")
		}
		return template.URL("?" + q)
	},
	"repoQuery": func(page int, repo, branchFilter string) template.URL {
		v := url.Values{}
		if page > 1 {
			v.Set("page", strconv.Itoa(page))
		}
		if branchFilter != "" {
			v.Set("branch_filter", branchFilter)
		}
		q := v.Encode()
		if q == "" {
			return template.URL("?")
		}
		return template.URL("?" + q)
	},
}).Parse(layoutHTML + homeHTML + repoHTML))

type layoutData struct {
	Site    SiteInfo
	Home    *HomePage
	Repo    *RepoPage
	CSS     template.CSS
	ThemeJS template.JS
	PageJS  template.JS
}

// Render live row HTML fragment.
func RenderHomeLiveRows(runs []HomeRunRow) ([]byte, error) {
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "home-live-rows", runs); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Render home page HTML.
func RenderHome(page HomePage) ([]byte, error) {
	data := layoutData{
		Site:    page.Site,
		Home:    &page,
		CSS:     template.CSS(styleCSS),
		ThemeJS: template.JS(themeJS),
		PageJS:  template.JS(homeJS),
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Render repo page HTML.
func RenderRepo(page RepoPage) ([]byte, error) {
	data := layoutData{
		Site:    page.Site,
		Repo:    &page,
		CSS:     template.CSS(styleCSS),
		ThemeJS: template.JS(themeJS),
		PageJS:  template.JS(repoJS),
	}
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
