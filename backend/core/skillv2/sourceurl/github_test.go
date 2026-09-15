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
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/example/skills" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
			return
		}
		const commitsPrefix = "/repos/example/skills/commits/"
		if strings.HasPrefix(request.URL.EscapedPath(), commitsPrefix) {
			ref, err := url.PathUnescape(strings.TrimPrefix(request.URL.EscapedPath(), commitsPrefix))
			if err == nil && map[string]bool{"main": true, "feature/foo": true, "v1.2.3": true}[ref] {
				w.WriteHeader(http.StatusOK)
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
			wantDownload: "https://github.com/example/skills/archive/main.zip",
		},
		{
			name:         "tree subdirectory",
			rawURL:       "https://github.com/example/skills/tree/main/skills/target",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/main.zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "branch containing slash",
			rawURL:       "https://github.com/example/skills/tree/feature/foo/skills/target",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/feature%2Ffoo.zip",
			wantPrefix:   "skills/target",
		},
		{
			name:         "tag subdirectory",
			rawURL:       "https://github.com/example/skills/tree/v1.2.3/skills/target",
			wantMatched:  true,
			wantDownload: "https://github.com/example/skills/archive/v1.2.3.zip",
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

func TestResolveGitHubPageURLFromResolvedArchive(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		resolvedURL  string
		wantMatched  bool
		wantErr      bool
		wantDownload string
		wantPrefix   string
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
		})
	}
}
