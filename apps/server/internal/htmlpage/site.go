package htmlpage

// Site footer metadata.
type SiteInfo struct {
	ServerVersion      string
	CommitSHA          string
	Environment        string
	BuildTime          string
	RepoURL            string
	GitHubOAuthEnabled bool
}
