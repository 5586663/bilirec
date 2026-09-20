package updatecheck

import (
	"os"
	"strings"
)

const (
	defaultGitHubAPIBase = "https://api.github.com/"

	// releasePageURL is the human-facing release link in /version and startup banners.
	// Not derived from GITHUB_API_URL. Point at
	// release.bilirec.org here when the mirror is live.
	releasePageURL = "https://github.com/bilirec/bilirec/releases/latest"
)

// NormalizeGitHubAPIBaseURL trims GITHUB_API_URL and ensures a trailing slash.
// It does not rewrite proxy URL shape (prefix vs host mirror); use the full
// go-github BaseURL your provider documents. Empty input means official API.
func NormalizeGitHubAPIBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}

func githubAPIBaseURL() string {
	v, ok := os.LookupEnv("GITHUB_API_URL")
	if !ok {
		return ""
	}
	normalized := NormalizeGitHubAPIBaseURL(v)
	if normalized == "" || strings.EqualFold(normalized, defaultGitHubAPIBase) {
		return ""
	}
	return normalized
}

