package updatecheck

import "testing"

func TestNormalizeGitHubAPIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"official", "https://api.github.com", defaultGitHubAPIBase},
		{"prefix full", "https://gh-proxy.com/https://api.github.com/", "https://gh-proxy.com/https://api.github.com/"},
		{"host mirror", "https://mirror.example.com", "https://mirror.example.com/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeGitHubAPIBaseURL(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeGitHubAPIBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGithubAPIBaseURLFromEnv(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	if githubAPIBaseURL() != "" {
		t.Fatal("expected empty base when env unset")
	}

	t.Setenv("GITHUB_API_URL", "https://api.github.com/")
	if githubAPIBaseURL() != "" {
		t.Fatal("expected empty base for official URL")
	}

	t.Setenv("GITHUB_API_URL", "https://gh-proxy.org/https://api.github.com/")
	got := githubAPIBaseURL()
	if got != "https://gh-proxy.org/https://api.github.com/" {
		t.Fatalf("unexpected base: %q", got)
	}
}
