package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	skillhttperr "lazymind/core/skillv2/httperr"
	skillpackage "lazymind/core/skillv2/skillpackage"
)

const marketGitHubCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func marketGitHubEntry(name, mode, kind, sha string, size int64) map[string]any {
	return map[string]any{"path": name, "mode": mode, "type": kind, "sha": sha, "size": size}
}
func marketGitHubTree(entries ...map[string]any) map[string]any {
	return map[string]any{"sha": "tree-sha", "truncated": false, "tree": entries}
}
func marketGitHubArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, contents := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(entry, contents)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func withMarketGitHubHTTPFixture(t *testing.T, routes map[string]any) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Host + r.URL.RequestURI()
		value, ok := routes[key]
		if !ok {
			if strings.Contains(r.URL.Path, "/commits/") || strings.HasSuffix(r.URL.Path, ".git/info/refs") {
				http.NotFound(w, r)
				return
			}
			t.Errorf("unexpected GitHub market request %s", key)
			http.NotFound(w, r)
			return
		}
		switch value := value.(type) {
		case int:
			w.WriteHeader(value)
		case string:
			_, _ = io.WriteString(w, value)
		case []byte:
			_, _ = w.Write(value)
		default:
			_ = json.NewEncoder(w).Encode(value)
		}
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Local fixture terminates all test GitHub hosts.
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
}

func TestMarketHTTPDownloaderImportsGitHubSources(t *testing.T) {
	tests := []struct {
		name         string
		directory    bool
		explicitRoot bool
		fallback     bool
	}{
		{name: "root"}, {name: "directory", directory: true}, {name: "explicit-root", explicitRoot: true}, {name: "fallback-directory", directory: true, fallback: true}, {name: "fallback-root", fallback: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := "market-" + test.name
			api := "api.github.com/repos/acme/" + repo
			rawURL := "https://github.com/acme/" + repo
			routes := map[string]any{api: map[string]any{"default_branch": "main"}, api + "/commits/main": map[string]any{"sha": marketGitHubCommit}}
			expected := map[string][]byte{"SKILL.md": []byte("skill"), "data.txt": []byte("data")}
			if test.explicitRoot {
				rawURL += "/blob/main/SKILL.md"
				routes[api+"/git/trees/"+marketGitHubCommit+"?recursive=1"] = marketGitHubTree(marketGitHubEntry("SKILL.md", "100644", "blob", "skill", 5), marketGitHubEntry("data.txt", "100644", "blob", "data", 4), marketGitHubEntry("examples/SKILL.md", "100644", "blob", "example", 7))
				routes["raw.githubusercontent.com/acme/"+repo+"/"+marketGitHubCommit+"/SKILL.md"] = "skill"
				routes["raw.githubusercontent.com/acme/"+repo+"/"+marketGitHubCommit+"/data.txt"] = "data"
				routes["raw.githubusercontent.com/acme/"+repo+"/"+marketGitHubCommit+"/examples/SKILL.md"] = "example"
				expected["examples/SKILL.md"] = []byte("example")
			} else if test.directory {
				rawURL += "/tree/main/skills/selected"
				routes[api+"/git/trees/"+marketGitHubCommit] = marketGitHubTree(marketGitHubEntry("skills", "040000", "tree", "skills-tree", 0))
				routes[api+"/git/trees/skills-tree"] = marketGitHubTree(marketGitHubEntry("selected", "040000", "tree", "selected-tree", 0), marketGitHubEntry("sibling", "040000", "tree", "sibling", 0))
				routes[api+"/git/trees/selected-tree?recursive=1"] = marketGitHubTree(marketGitHubEntry("SKILL.md", "100644", "blob", "skill", 5), marketGitHubEntry("data.txt", "100644", "blob", "data", 4))
			} else {
				routes[api+"/git/trees/"+marketGitHubCommit+"?recursive=1"] = marketGitHubTree(marketGitHubEntry("README.md", "100644", "blob", "readme", 99), marketGitHubEntry("skills/selected/SKILL.md", "100644", "blob", "skill", 5), marketGitHubEntry("skills/selected/data.txt", "100644", "blob", "data", 4))
			}
			if !test.explicitRoot {
				routes["raw.githubusercontent.com/acme/"+repo+"/"+marketGitHubCommit+"/skills/selected/SKILL.md"] = "skill"
				routes["raw.githubusercontent.com/acme/"+repo+"/"+marketGitHubCommit+"/skills/selected/data.txt"] = "data"
			}
			if test.fallback {
				treeURL := api + "/git/trees/" + marketGitHubCommit
				if !test.directory {
					treeURL += "?recursive=1"
				}
				routes[treeURL] = http.StatusForbidden
				files := map[string]string{"repo-main/README.md": "readme", "repo-main/skills/selected/SKILL.md": "skill", "repo-main/skills/selected/data.txt": "data"}
				if test.directory {
					files["repo-main/skills/sibling/SKILL.md"] = "sibling"
				}
				routes["github.com/acme/"+repo+"/archive/"+marketGitHubCommit+".zip"] = marketGitHubArchive(t, files)
			}
			withMarketGitHubHTTPFixture(t, routes)
			archive, err := (marketHTTPZipDownloader{}).Download(context.Background(), rawURL)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archive)
			pkg, err := skillpackage.ReadZip(archive)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pkg.Files, expected) {
				t.Fatalf("files=%v expected=%v", pkg.Files, expected)
			}
		})
	}
}

func TestMarketHTTPDownloaderReportsGitHubSemanticErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []map[string]any
		status  int
		code    string
	}{
		{name: "multiple", entries: []map[string]any{marketGitHubEntry("one/SKILL.md", "100644", "blob", "one", 1), marketGitHubEntry("two/SKILL.md", "100644", "blob", "two", 1)}, status: 400, code: skillhttperr.CodeGitHubMultipleSkills},
		{name: "large", entries: []map[string]any{marketGitHubEntry("SKILL.md", "100644", "blob", "large", (32<<20)+1)}, status: 413, code: skillhttperr.CodePayloadTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := "market-error-" + test.name
			api := "api.github.com/repos/acme/" + repo
			withMarketGitHubHTTPFixture(t, map[string]any{api: map[string]any{"default_branch": "main"}, api + "/commits/main": map[string]any{"sha": marketGitHubCommit}, api + "/git/trees/" + marketGitHubCommit + "?recursive=1": marketGitHubTree(test.entries...)})
			archive, err := (marketHTTPZipDownloader{}).Download(context.Background(), "https://github.com/acme/"+repo)
			if archive != "" {
				defer os.Remove(archive)
			}
			if err == nil {
				t.Fatal("expected GitHub semantic error")
			}
			sem := skillhttperr.ForError(err)
			if sem.Status != test.status || sem.Code != test.code {
				t.Fatalf("error=%+v", sem)
			}
		})
	}
}
