package sourceurl

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGitHubMetadataCacheIsolatesCustomHTTPClients(t *testing.T) {
	client := func(body string) *http.Client {
		return &http.Client{Transport: githubTestRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
	}
	for i, transport := range []*http.Client{client(`{"sha":"one"}`), client(`{"sha":"two"}`)} {
		var got struct{ SHA string }
		if _, err := GitHubAPIGet(context.Background(), transport, "https://cache-isolation.test/repo", &got); err != nil {
			t.Fatal(err)
		}
		if got.SHA != []string{"one", "two"}[i] {
			t.Fatalf("custom client received another transport's cached response: %s", got.SHA)
		}
	}
}

func TestResolveGitHubRateLimitFallsBackToValidatedArchiveRefs(t *testing.T) {
	t.Setenv("LAZYMIND_GITHUB_TOKEN", "test-token")
	for _, tt := range []struct{ source, validRef, wantPath string }{
		{"https://github.com/acme/repo", "HEAD", ""},
		{"https://github.com/acme/repo/tree/feature/foo/skills/demo", "feature/foo", "skills/demo"},
		{"https://github.com/acme/repo/blob/feature/foo/skills/demo/SKILL.md?plain=1#usage", "feature/foo", "skills/demo"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			heads := 0
			client := &http.Client{Transport: githubTestRoundTripper(func(r *http.Request) (*http.Response, error) {
				status := 404
				headers := make(http.Header)
				if r.URL.Hostname() == "api.github.com" {
					status = 403
					headers.Set("X-RateLimit-Remaining", "0")
				} else {
					if r.Method != http.MethodHead || r.Header.Get("Authorization") != "" {
						t.Errorf("archive validation must be unauthenticated HEAD: %s %s", r.Method, r.URL)
					}
					heads++
					if r.URL.String() == githubArchiveURL("acme", "repo", tt.validRef) {
						status = 200
					}
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})}
			parsed, _ := url.Parse(tt.source)
			if _, _, err := ResolveGitHubPageURL(context.Background(), parsed, client, GitHubAPIBaseURL); err == nil || heads != 0 {
				t.Fatalf("strict lock resolution must stay pinned: error=%v HEADs=%d", err, heads)
			}
			got, matched, err := ResolveGitHubImportURL(context.Background(), parsed, client, GitHubAPIBaseURL)
			if err != nil || !matched || got.Revision != tt.validRef || got.PathPrefix != tt.wantPath || heads == 0 {
				t.Fatalf("resolution=%#v matched=%v error=%v HEADs=%d", got, matched, err, heads)
			}
		})
	}
}

func TestGitHubRootDocumentIsAnExplicitDirectory(t *testing.T) {
	for _, raw := range []string{"https://github.com/acme/repo/blob/main/SKILL.md", "https://raw.githubusercontent.com/acme/repo/main/SKILL.md", "https://github.com/acme/repo/tree/main"} {
		parsed, _ := url.Parse(raw)
		got, _, err := ResolveGitHubPageURLFromResolvedArchive(parsed, "https://github.com/acme/repo/archive/1111111111111111111111111111111111111111.zip")
		if err != nil || !got.ExplicitDirectory {
			t.Fatalf("%s should select a directory: %#v %v", raw, got, err)
		}
	}
}
