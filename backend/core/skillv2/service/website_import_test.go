package service

import (
	"context"
	"reflect"
	"testing"
)

func TestWebsiteImportDownloadsResolvedGitHubDirectory(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit: packageTree(packageEntry("demo", "040000", "tree", "demo-tree", 0)),
		"/api/repos/acme/repo/git/trees/demo-tree?recursive=1": packageTree(
			packageEntry("SKILL.md", "100644", "blob", "document", 4),
			packageEntry("logo.png", "100644", "blob", "image", 3),
		),
	}, map[string]string{
		"/raw/acme/repo/" + packageCommit + "/demo/SKILL.md": "demo",
		"/raw/acme/repo/" + packageCommit + "/demo/logo.png": "\x89\x00\xff",
	})
	source := packageSource("demo")
	source.ResolvedSourceURL = source.SourceURL
	source.SourceURL = "https://skills.sh/acme/repo/demo?from=search#install"
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got := packageZipContents(t, downloaded); !reflect.DeepEqual(got, map[string]string{"SKILL.md": "demo", "logo.png": "\x89\x00\xff"}) {
		t.Fatalf("website import lost directory files: %#v", got)
	}
}
