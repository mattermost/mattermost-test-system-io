package htmlpage

// SiteInfo is site footer metadata.
type SiteInfo struct {
	ServerVersion      string
	CommitSHA          string
	Environment        string
	BuildTime          string
	RepoURL            string
	GitHubOAuthEnabled bool
}
