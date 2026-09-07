package htmlpage

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func githubRepo(url string) string {
	url = trimPrefix(url, "https://")
	url = trimPrefix(url, "http://")
	return trimPrefix(url, "github.com/")
}

func trimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
