package sourceurl

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

const skillsSHTestCommit = "1234567890123456789012345678901234567890"

// Replacing frontmatter matching with a guessed slug directory breaks this
// regression: the published name deliberately differs from the directory.
func TestResolveSkillsSHPageURLMatchesPublishedFrontmatterName(t *testing.T) {
	client, calls := skillsSHTestClient(t, map[string]string{
		"www.skills.sh/example/skills/vercel-react-best-practices":                                                 `<a href="https://github.com/example/skills">example/skills</a>`,
		"api.github.com/repos/example/skills/commits/HEAD":                                                         `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1":                     `{"tree":[{"path":"skills/unrelated/SKILL.md","type":"blob","mode":"100644"},{"path":"skills/react-best-practices/SKILL.md","type":"blob","mode":"100644"}]}`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/skills/react-best-practices/SKILL.md": "---\nname: vercel-react-best-practices\ndescription: React rules\n---\n# Rules",
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/skills/unrelated/SKILL.md":            "---\nname: unrelated\ndescription: Other rules\n---\n# Other",
	})
	parsed, _ := url.Parse("https://www.skills.sh/example/skills/vercel-react-best-practices?utm_source=test#install")
	got, matched, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || !matched {
		t.Fatalf("ResolveSkillsSHPageURL() matched=%v err=%v", matched, err)
	}
	if got.GitHubSourceURL != "https://github.com/example/skills/tree/"+skillsSHTestCommit+"/skills/react-best-practices" || got.CanonicalName != "vercel-react-best-practices" {
		t.Fatalf("unexpected resolution: %+v", got)
	}
	if len(*calls) != 5 {
		t.Fatalf("expected complete bounded name discovery, got %v", *calls)
	}
}

func TestResolveSkillsSHPageURLUsesVerifiedExactPageLink(t *testing.T) {
	client, calls := skillsSHTestClient(t, map[string]string{
		"www.skills.sh/example/skills/react-rules":                                                        `<a href="https://github.com/other/repo/tree/main/react-rules">other</a><a href="https://github.com/example/skills/blob/` + skillsSHTestCommit + `/catalog/unexpected/SKILL.md?plain=1#rules">Source</a>`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/catalog/unexpected/SKILL.md": "---\nname: React_Rules\ndescription: Rules\n---\n# Rules",
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/react-rules")
	got, matched, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || !matched || got.GitHubSourceURL != "https://github.com/example/skills/tree/"+skillsSHTestCommit+"/catalog/unexpected" || got.CanonicalName != "React_Rules" {
		t.Fatalf("resolution=%+v matched=%v err=%v", got, matched, err)
	}
	if len(*calls) != 2 {
		t.Fatalf("page link should avoid tree discovery, calls=%v", *calls)
	}
}

func TestResolveSkillsSHPageURLScansNamesWhenDirectoriesDiffer(t *testing.T) {
	client, _ := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                                       `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1":   `{"tree":[{"path":"catalog/a/SKILL.md","type":"blob","mode":"100644"},{"path":"catalog/b/SKILL.md","type":"blob","mode":"100644"}]}`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/catalog/a/SKILL.md": "---\nname: Other\ndescription: Other\n---\n",
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/catalog/b/SKILL.md": "---\nname: 'React & Next Rules'\ndescription: Rules\n---\n",
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/react-next-rules")
	got, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || got.GitHubSourceURL != "https://github.com/example/skills/tree/"+skillsSHTestCommit+"/catalog/b" {
		t.Fatalf("resolution=%+v err=%v", got, err)
	}
}

func TestResolveSkillsSHPageURLRejectsUnsafeOrUnrecognizedURLs(t *testing.T) {
	for _, tt := range []struct {
		raw              string
		matched, wantErr bool
	}{
		{"https://example.com/owner/repo/skill", false, false},
		{"https://skills.sh/owner/repo", true, true},
		{"https://skills.sh/owner/repo/skill/extra", true, true},
		{"https://skills.sh/owner/repo/%2e%2e", true, true},
		{"https://skills.sh/owner/repo/a%2fb", true, true},
		{"https://skills.sh:443/owner/repo/skill", true, true},
		{"https://user@skills.sh/owner/repo/skill", true, true},
		{"ftp://skills.sh/owner/repo/skill", true, true},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			parsed, _ := url.Parse(tt.raw)
			client, calls := skillsSHTestClient(t, nil)
			_, matched, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
			if matched != tt.matched || (err != nil) != tt.wantErr {
				t.Fatalf("matched=%v err=%v", matched, err)
			}
			if len(*calls) != 0 {
				t.Fatalf("invalid input issued requests: %v", *calls)
			}
		})
	}
}

func TestResolveSkillsSHPageURLRejectsIncompleteAmbiguousAndMissingSkills(t *testing.T) {
	for _, tt := range []struct {
		name, tree, wantErr string
		documents           map[string]string
	}{
		{"truncated tree", `{"truncated":true,"tree":[{"path":"target/SKILL.md","type":"blob","mode":"100644"}]}`, "truncated", nil},
		{"missing skill", `{"tree":[{"path":"other/SKILL.md","type":"blob","mode":"100644"}]}`, "not found", map[string]string{"other/SKILL.md": "---\nname: Other\ndescription: Other\n---\n"}},
		{"ambiguous name", `{"tree":[{"path":"one/SKILL.md","type":"blob","mode":"100644"},{"path":"two/SKILL.md","type":"blob","mode":"100644"}]}`, "ambiguous", map[string]string{"one/SKILL.md": "---\nname: target\ndescription: One\n---\n", "two/SKILL.md": "---\nname: target\ndescription: Two\n---\n"}},
		{"unsafe tree path", `{"tree":[{"path":"../target/SKILL.md","type":"blob","mode":"100644"}]}`, "unsafe", nil},
		{"oversized frontmatter", `{"tree":[{"path":"target/SKILL.md","type":"blob","mode":"100644"}]}`, "exceeds", map[string]string{"target/SKILL.md": "---\nname: target\n" + strings.Repeat("x", 128*1024+1)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			responses := map[string]string{
				"api.github.com/repos/example/skills/commits/HEAD":                                     `{"sha":"` + skillsSHTestCommit + `"}`,
				"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1": tt.tree,
			}
			for path, body := range tt.documents {
				responses["raw.githubusercontent.com/example/skills/"+skillsSHTestCommit+"/"+path] = body
			}
			client, _ := skillsSHTestClient(t, responses)
			parsed, _ := url.Parse("https://skills.sh/example/skills/target")
			_, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want %q error, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestResolveSkillsSHPageURLIgnoresNestedExamplesAndBuildArtifacts(t *testing.T) {
	client, calls := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                                           `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1":       `{"tree":[{"path":"node_modules/target/SKILL.md","type":"blob","mode":"100644"},{"path":"skills/parent/skill.MD","type":"blob","mode":"100644"},{"path":"skills/parent/examples/target/SKILL.md","type":"blob","mode":"100644"},{"path":"skills/real/SKILL.md","type":"blob","mode":"100644"}]}`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/skills/parent/skill.MD": "---\nname: parent\ndescription: Parent\n---\n",
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/skills/real/SKILL.md":   "---\nname: target\ndescription: Target\n---\n",
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	got, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || got.GitHubSourceURL != "https://github.com/example/skills/tree/"+skillsSHTestCommit+"/skills/real" {
		t.Fatalf("resolution=%+v err=%v calls=%v", got, err, *calls)
	}
}

func TestResolveSkillsSHPageURLBoundsDiscoveryAndHonorsCancellation(t *testing.T) {
	var tree strings.Builder
	tree.WriteString(`{"tree":[`)
	for index := 0; index < 129; index++ {
		if index > 0 {
			tree.WriteByte(',')
		}
		fmt.Fprintf(&tree, `{"path":"catalog/item-%d/SKILL.md","type":"blob","mode":"100644"}`, index)
	}
	tree.WriteString(`]}`)
	client, calls := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                                     `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1": tree.String(),
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	_, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err == nil || !strings.Contains(err.Error(), "exceeds") || len(*calls) != 4 {
		t.Fatalf("expected bounded discovery error before document fetches: err=%v calls=%v", err, *calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = ResolveSkillsSHPageURL(ctx, parsed, client, GitHubAPIBaseURL)
	if err != context.Canceled || len(*calls) != 4 {
		t.Fatalf("cancellation err=%v calls=%v", err, *calls)
	}
}

func TestResolveSkillsSHPageURLRejectsUntrustedRedirects(t *testing.T) {
	var visited []string
	client := &http.Client{Transport: skillsSHTestRoundTripper(func(request *http.Request) (*http.Response, error) {
		visited = append(visited, request.URL.Hostname())
		if request.URL.Hostname() == "www.skills.sh" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://internal.example/secret"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not found")), Request: request}, nil
	})}
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	_, _, _ = ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	for _, host := range visited {
		if host == "internal.example" {
			t.Fatalf("followed untrusted redirect: %v", visited)
		}
	}
}

func TestResolveSkillsSHPageURLRejectsDuplicateNamesAcrossDirectoryPriorities(t *testing.T) {
	client, _ := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                                               `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1":           `{"tree":[{"path":"skills/target/SKILL.md","type":"blob","mode":"100644"},{"path":"catalog/arbitrary/SKILL.md","type":"blob","mode":"100644"}]}`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/skills/target/SKILL.md":     "---\nname: target\ndescription: Target\n---\n",
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/catalog/arbitrary/SKILL.md": "---\nname: target\ndescription: Another target\n---\n",
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	_, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate canonical names require exact GitHub URL: %v", err)
	}
}

func TestResolveSkillsSHPageURLRejectsDuplicateNamesInPageLinks(t *testing.T) {
	client, _ := skillsSHTestClient(t, map[string]string{
		"www.skills.sh/example/skills/target":                                                            `<a href="https://github.com/example/skills/blob/` + skillsSHTestCommit + `/skills/target/SKILL.md">Source</a><a href="https://github.com/example/skills/blob/` + skillsSHTestCommit + `/catalog/arbitrary/SKILL.md">Another</a>`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/skills/target/SKILL.md":     "---\nname: target\ndescription: Target\n---\n",
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/catalog/arbitrary/SKILL.md": "---\nname: target\ndescription: Another target\n---\n",
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	_, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate source links require exact GitHub URL: %v", err)
	}
}

func TestResolveSkillsSHPageURLRateLimitedArchiveFallbackKeepsRealDirectory(t *testing.T) {
	head := skillsSHTestCommit + " HEAD\x00symref=HEAD:refs/heads/release\n"
	client, _ := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                `{"message":"API rate limit exceeded"}`,
		"github.com/example/skills.git/info/refs?service=git-upload-pack": "001e# service=git-upload-pack\n0000" + fmt.Sprintf("%04x%s", len(head)+4, head) + "0000",
		"codeload.github.com/example/skills/zip/" + skillsSHTestCommit: skillsSHArchiveFixture(t, map[string]string{
			"skills-root/catalog/unexpected/SKILL.md":       "---\nname: published-name\ndescription: Target\n---\n# Target",
			"skills-root/skills/another/SKILL.md":           "---\nname: another\ndescription: Other\n---\n",
			"skills-root/catalog/unexpected/scripts/run.py": "print('kept by downloader')",
		}),
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/published-name")
	got, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || got.GitHubSourceURL != "https://github.com/example/skills/tree/"+skillsSHTestCommit+"/catalog/unexpected" {
		t.Fatalf("resolution=%+v err=%v", got, err)
	}
}

func TestSkillsSHArchiveLimitUsesRecoverablePackageSizeError(t *testing.T) {
	client := &http.Client{Transport: skillsSHTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: 100<<20 + 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	_, _, err := resolveSkillsSHArchive(context.Background(), client, "example", "skills", "target", skillsSHTestCommit, fmt.Errorf("GitHub API rate limit reached"))
	if err == nil || !strings.Contains(err.Error(), "skill package download exceeds") {
		t.Fatalf("archive size failure must use the import-size category for Chat recovery: %v", err)
	}
}

func TestResolveSkillsSHPageURLArchiveScansBeyondHTTPDocumentLimit(t *testing.T) {
	files := map[string]string{}
	for index := 0; index < 130; index++ {
		name := fmt.Sprintf("another-%d", index)
		if index == 0 || index == 129 {
			name = "target"
		}
		files[fmt.Sprintf("root/catalog/skill-%03d/SKILL.md", index)] = "---\nname: " + name + "\ndescription: Skill\n---\n"
	}
	head := skillsSHTestCommit + " HEAD\x00multi_ack\n"
	client, _ := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                `{"message":"API rate limit exceeded"}`,
		"github.com/example/skills.git/info/refs?service=git-upload-pack": "001e# service=git-upload-pack\n0000" + fmt.Sprintf("%04x%s", len(head)+4, head),
		"codeload.github.com/example/skills/zip/" + skillsSHTestCommit:    skillsSHArchiveFixture(t, files),
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	_, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("archive must scan complete candidate names: %v", err)
	}
}

func TestResolveSkillsSHPageURLReadsOnlyLargeDocumentFrontmatter(t *testing.T) {
	client, _ := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                                     `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1": `{"tree":[{"path":"target/SKILL.md","type":"blob","mode":"100644"}]}`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/target/SKILL.md":  "---\nname: target\ndescription: Large skill\n---\n" + strings.Repeat("body\n", 40000),
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	got, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || got.CanonicalName != "target" {
		t.Fatalf("resolution=%+v err=%v", got, err)
	}
}

func TestResolveSkillsSHPageURLArchiveRejectsUnsafePathsAndSymlinkDocuments(t *testing.T) {
	var symlink bytes.Buffer
	writer := zip.NewWriter(&symlink)
	header := &zip.FileHeader{Name: "root/target/SKILL.md", Method: zip.Deflate}
	header.SetMode(os.ModeSymlink | 0777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(entry, "../../outside")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, archive, want string }{
		{"unsafe path", skillsSHArchiveFixture(t, map[string]string{"root/../target/SKILL.md": "---\nname: target\ndescription: Target\n---\n"}), "unsafe"},
		{"symlink document", symlink.String(), "symlink"},
		{"mixed roots", skillsSHArchiveFixture(t, map[string]string{"root/target/SKILL.md": "name", "another/README": "readme"}), "one repository root"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := skillsSHTestClient(t, map[string]string{
				"api.github.com/repos/example/skills/commits/HEAD":                                     `{"sha":"` + skillsSHTestCommit + `"}`,
				"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1": `{"truncated":true,"tree":[]}`,
				"codeload.github.com/example/skills/zip/" + skillsSHTestCommit:                         tt.archive,
			})
			parsed, _ := url.Parse("https://skills.sh/example/skills/target")
			_, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("wanted %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestSkillsSHGitAdvertisementRejectsMalformedAndUnpinnedRefs(t *testing.T) {
	for _, payload := range []string{"zzzz", "0003", "002f0000000000000000000000000000000000000000 HEAD\n", "002f1234567890123456789012345678901234567890 tags\n"} {
		client, _ := skillsSHTestClient(t, map[string]string{"github.com/example/skills.git/info/refs?service=git-upload-pack": payload})
		if revision, err := skillsSHAdvertisedHead(context.Background(), client, "example", "skills"); err == nil || revision != "" {
			t.Fatalf("accepted malformed or non-HEAD advertisement %q: revision=%q err=%v", payload, revision, err)
		}
	}
}

func TestSkillsSHSlugMatchesCatalogNormalization(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"React & Next Rules", "react-next-rules"},
		{"Hello___  World!!", "hello-world"},
		{"--A/B--", "ab"},
		{"a\u00a0b", "a-b"},
		{"a\ufeffb", "a-b"},
	} {
		if got := skillsSHSlug(tt.name); got != tt.want {
			t.Errorf("skillsSHSlug(%q)=%q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestResolveSkillsSHPageURLKeepsDistinctChildAlongsideRootEntry(t *testing.T) {
	client, _ := skillsSHTestClient(t, map[string]string{
		"api.github.com/repos/example/skills/commits/HEAD":                                     `{"sha":"` + skillsSHTestCommit + `"}`,
		"api.github.com/repos/example/skills/git/trees/" + skillsSHTestCommit + "?recursive=1": `{"tree":[{"path":"SKILL.md","type":"blob","mode":"100644"},{"path":"target/SKILL.md","type":"blob","mode":"100644"}]}`,
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/SKILL.md":         "---\nname: root-skill\ndescription: Root skill\n---\n",
		"raw.githubusercontent.com/example/skills/" + skillsSHTestCommit + "/target/SKILL.md":  "---\nname: target\ndescription: Child skill\n---\n",
	})
	parsed, _ := url.Parse("https://skills.sh/example/skills/target")
	got, _, err := ResolveSkillsSHPageURL(context.Background(), parsed, client, GitHubAPIBaseURL)
	if err != nil || got.GitHubSourceURL != "https://github.com/example/skills/tree/"+skillsSHTestCommit+"/target" {
		t.Fatalf("resolution=%+v err=%v", got, err)
	}
}

func skillsSHArchiveFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}

func skillsSHTestClient(t *testing.T, responses map[string]string) (*http.Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	client := &http.Client{Transport: skillsSHTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		key := r.URL.Hostname() + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		mu.Lock()
		calls = append(calls, key)
		mu.Unlock()
		status, body := http.StatusOK, responses[key]
		if _, ok := responses[key]; !ok {
			status, body = http.StatusNotFound, "not found"
		}
		header := make(http.Header)
		if body == `{"message":"API rate limit exceeded"}` {
			status = http.StatusForbidden
			header.Set("X-RateLimit-Remaining", "0")
		}
		if r.Header.Get("Authorization") != "" && r.URL.Hostname() != "api.github.com" {
			return nil, fmt.Errorf("credential leaked to %s", r.URL.Hostname())
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: header, Request: r}, nil
	})}
	return client, &calls
}

type skillsSHTestRoundTripper func(*http.Request) (*http.Response, error)

func (f skillsSHTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
