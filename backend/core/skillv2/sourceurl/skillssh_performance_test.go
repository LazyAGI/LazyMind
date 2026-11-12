package sourceurl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSkillsSHDiscoversNamesWithBoundedParallelRequests(t *testing.T) {
	var active, peak atomic.Int32
	var entries []map[string]string
	for i := 0; i < 16; i++ {
		entries = append(entries, map[string]string{"path": fmt.Sprintf("skills/%02d/SKILL.md", i), "type": "blob", "mode": "100644"})
	}
	tree, _ := json.Marshal(map[string]any{"tree": entries})
	client := &http.Client{Transport: skillsSHTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := "<html></html>"
		if r.URL.Hostname() == "api.github.com" {
			if strings.Contains(r.URL.Path, "/commits/") {
				body = `{"sha":"` + skillsSHTestCommit + `"}`
			} else {
				body = string(tree)
			}
		}
		if r.URL.Hostname() == "raw.githubusercontent.com" {
			now := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); now > old; old = peak.Load() {
				if peak.CompareAndSwap(old, now) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			name := "other"
			if strings.Contains(r.URL.Path, "/00/") {
				name = "wanted"
			}
			body = "---\nname: " + name + "\ndescription: description\n---\n"
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	parsed, _ := url.Parse("https://skills.sh/acme/repo/wanted")
	start := time.Now()
	got, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || got.CanonicalName != "wanted" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	t.Logf("16 docs @20ms: elapsed=%s peak=%d", time.Since(start), peak.Load())
	if peak.Load() < 2 || peak.Load() > 8 {
		t.Fatalf("discovery concurrency=%d, want bounded parallel fetching", peak.Load())
	}
}

func TestGitHubImportResolvesSlashRefsWithOneAdvertisement(t *testing.T) {
	var calls atomic.Int32
	refs := "001e# service=git-upload-pack\n0000"
	for _, line := range []string{skillsSHTestCommit + " HEAD\x00symref=HEAD:refs/heads/main\n", skillsSHTestCommit + " refs/heads/main\n", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa refs/heads/main/skills\n"} {
		refs += fmt.Sprintf("%04x%s", len(line)+4, line)
	}
	refs += "0000"
	client := &http.Client{Transport: skillsSHTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if !strings.HasSuffix(r.URL.Path, ".git/info/refs") {
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(refs)), Request: r}, nil
	})}
	parsed, _ := url.Parse("https://github.com/acme/repo/tree/main/skills/a/b/c/d/demo")
	got, matched, err := ResolveGitHubImportURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || !matched || got.PathPrefix != "a/b/c/d/demo" || got.Revision != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || calls.Load() != 1 {
		t.Fatalf("result=%+v matched=%v err=%v requests=%d", got, matched, err, calls.Load())
	}
}

func TestSkillsSHParallelDiscoveryHonorsCancellation(t *testing.T) {
	started := make(chan struct{}, 8)
	client := &http.Client{Transport: skillsSHTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := "<html></html>"
		if r.URL.Hostname() == "api.github.com" {
			if strings.Contains(r.URL.Path, "/commits/") {
				body = `{"sha":"` + skillsSHTestCommit + `"}`
			} else {
				body = `{"tree":[{"path":"one/SKILL.md","type":"blob","mode":"100644"},{"path":"two/SKILL.md","type":"blob","mode":"100644"}]}`
			}
		}
		if r.URL.Hostname() == "raw.githubusercontent.com" {
			started <- struct{}{}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	parsed, _ := url.Parse("https://skills.sh/acme/repo/wanted")
	go func() { _, _, err := ResolveSkillsSHPageURL(ctx, parsed, client, GitHubAPIBaseURL); done <- err }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("document workers did not run concurrently")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("document cancellation did not finish")
	}
}
