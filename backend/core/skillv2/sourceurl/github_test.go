package sourceurl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestResolveGitHubPageURL(t *testing.T) {
	const mainCommit = "1111111111111111111111111111111111111111"
	const featureCommit = "2222222222222222222222222222222222222222"
	const tagCommit = "3333333333333333333333333333333333333333"
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/example/skills" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
			return
		}
		const commitsPrefix = "/repos/example/skills/commits/"
		if strings.HasPrefix(request.URL.EscapedPath(), commitsPrefix) {
			ref, err := url.PathUnescape(strings.TrimPrefix(request.URL.EscapedPath(), commitsPrefix))
			commits := map[string]string{"main": mainCommit, "feature/foo": featureCommit, "v1.2.3": tagCommit}
			if err == nil && commits[ref] != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"sha":"` + commits[ref] + `"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.NotFound(w, request)
	}))
	defer apiServer.Close()

	tests := []struct {
		name         string
		rawURL       string
		wantMatched  bool
		wantErr      bool
		wantDownload string
		wantPrefix   string
	}{
		{
			name:         "repository root",
			rawURL:       "https://github.com/example/skills",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/" + mainCommit + ".zip",
		},
		{
			name:         "tree subdirectory",
			rawURL:       "https://github.com/example/skills/tree/main/skills/target",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/" + mainCommit + ".zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "branch containing slash",
			rawURL:       "https://github.com/example/skills/tree/feature/foo/skills/target",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/" + featureCommit + ".zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "tag subdirectory",
			rawURL:       "https://github.com/example/skills/tree/v1.2.3/skills/target",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/" + tagCommit + ".zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "archive URL",
			rawURL:       "https://github.com/example/skills/archive/refs/heads/main.zip",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/main.zip",
		},
		{
			name:        "release ZIP remains direct",
			rawURL:      "https://github.com/example/skills/releases/download/v1.0.0/skill.zip",
			wantMatched: false,
		},
		{
			name:        "non GitHub URL",
			rawURL:      "https://example.test/skill.zip",
			wantMatched: false,
		},
		{
			name:        "tree missing skill path",
			rawURL:      "https://github.com/example/skills/tree/main",
			wantMatched: true,
			wantErr:     true,
		},
		{
			name:        "blob page",
			rawURL:      "https://github.com/example/skills/blob/main/SKILL.md",
			wantMatched: true,
			wantErr:     true,
		},
		{
			name:        "query is rejected",
			rawURL:      "https://github.com/example/skills?download=1",
			wantMatched: true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			got, matched, err := ResolveGitHubPageURL(context.Background(), parsed, apiServer.Client(), apiServer.URL)
			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveGitHubPageURL returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveGitHubPageURL returned error: %v", err)
			}
			if got.DownloadURL != tt.wantDownload || got.PathPrefix != tt.wantPrefix {
				t.Fatalf("resolution = %#v, want download URL %q and prefix %q", got, tt.wantDownload, tt.wantPrefix)
			}
		})
	}
}

func TestResolveFullCommitWithoutGitHubAPI(t *testing.T) {
	const commit = "2724fd2efd8c6737f6fa704fbf5da52d67375497"
	for _, ref := range []string{commit, strings.ToUpper(commit), commit[:12], strings.Repeat("g", 40), "feature/foo"} {
		t.Run(ref, func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusForbidden)
			}))
			defer api.Close()
			parsed, err := url.Parse("https://github.com/example/skills/tree/" + ref + "/skills/target")
			if err != nil {
				t.Fatal(err)
			}
			got, matched, err := ResolveGitHubPageURL(context.Background(), parsed, api.Client(), api.URL)
			if !matched {
				t.Fatal("GitHub tree URL was not recognized")
			}
			if ref == commit || ref == strings.ToUpper(commit) {
				if err != nil || calls != 0 || got.DownloadURL != "https://github.com/example/skills/archive/"+ref+".zip" || got.PathPrefix != "skills/target" {
					t.Fatalf("full commit resolution=%+v calls=%d err=%v", got, calls, err)
				}
			} else if err == nil || calls == 0 {
				t.Fatalf("ambiguous ref must retain API validation: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestResolveGitHubPageURLFromResolvedArchive(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		resolvedURL  string
		wantMatched  bool
		wantErr      bool
		wantDownload string
		wantPrefix   string
		wantPrefixes []string
	}{
		{
			name:         "repository root",
			rawURL:       "https://github.com/example/skills",
			resolvedURL:  "https://github.com/example/skills/archive/main.zip",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/main.zip",
		},
		{
			name:         "tree subdirectory",
			rawURL:       "https://github.com/example/skills/tree/main/skills/target",
			resolvedURL:  "https://github.com/example/skills/archive/main.zip",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/main.zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "branch containing slash",
			rawURL:       "https://github.com/example/skills/tree/feature/foo/skills/target",
			resolvedURL:  "https://github.com/example/skills/archive/feature%2Ffoo.zip",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/feature%2Ffoo.zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "branch containing slash resolved to locked commit",
			rawURL:       "https://github.com/example/skills/tree/feature/foo/skills/target",
			resolvedURL:  "https://github.com/example/skills/archive/1111111111111111111111111111111111111111.zip",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/1111111111111111111111111111111111111111.zip",
			wantPrefix:   "foo/skills/target",
			wantPrefixes: []string{"foo/skills/target", "skills/target", "target"},
		},
		{
			name:        "mismatched repository",
			rawURL:      "https://github.com/example/skills",
			resolvedURL: "https://github.com/other/skills/archive/main.zip",
			wantMatched: true,
			wantErr:     true,
		},
		{
			name:        "mismatched ref",
			rawURL:      "https://github.com/example/skills/tree/main/skills/target",
			resolvedURL: "https://github.com/example/skills/archive/develop.zip",
			wantMatched: true,
			wantErr:     true,
		},
		{
			name:        "non GitHub URL",
			rawURL:      "https://example.test/skill.zip",
			resolvedURL: "https://example.test/skill.zip",
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			got, matched, err := ResolveGitHubPageURLFromResolvedArchive(parsed, tt.resolvedURL)
			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveGitHubPageURLFromResolvedArchive returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveGitHubPageURLFromResolvedArchive returned error: %v", err)
			}
			if got.DownloadURL != tt.wantDownload || got.PathPrefix != tt.wantPrefix {
				t.Fatalf("resolution = %#v, want download URL %q and prefix %q", got, tt.wantDownload, tt.wantPrefix)
			}
			if tt.wantPrefixes != nil && strings.Join(got.PathPrefixCandidates, ",") != strings.Join(tt.wantPrefixes, ",") {
				t.Fatalf("prefix candidates = %#v, want %#v", got.PathPrefixCandidates, tt.wantPrefixes)
			}
		})
	}
}
