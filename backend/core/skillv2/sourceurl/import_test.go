package sourceurl

import (
	"context"
	"testing"
)

func TestResolveImportURLDispatchesBuiltInAdapters(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	for _, tt := range []struct{ input, adapter, download, prefix, name string }{
		{"https://example.test/package.zip?token=abc", "http_zip", "https://example.test/package.zip?token=abc", "", ""},
		{"https://skillhub.cn/skills/example/demo?utm_source=share#install", "skillhub", "https://api.skillhub.cn/api/v1/download?slug=%40example%2Fdemo", "", "demo"},
		{"https://clawhub.ai/example/skills/demo?utm_source=share#install", "clawhub", "https://clawhub.ai/api/v1/download?owner=example&slug=demo", "", "demo"},
		{"https://github.com/example/repo/blob/" + commit + "/skills/demo/SKILL.md?plain=1#usage", "github", "https://github.com/example/repo/archive/" + commit + ".zip", "skills/demo", ""},
	} {
		t.Run(tt.adapter, func(t *testing.T) {
			got, err := ResolveImportURL(context.Background(), tt.input, nil, GitHubAPIBaseURL)
			if err != nil || got.Adapter != tt.adapter || got.DownloadURL != tt.download || got.PathPrefix != tt.prefix || got.CanonicalName != tt.name {
				t.Fatalf("resolution=%#v error=%v", got, err)
			}
		})
	}
}

func TestResolveImportURLRejectsInvalidAndUnsupportedSitePages(t *testing.T) {
	for _, input := range []string{"/relative.zip", "ftp://example.test/skill.zip", "https://user:secret@example.test/skill.zip", "https://skillsmp.com/search?q=demo", "https://skills.sh/owner/repo", "https://clawhub.ai/search?q=demo"} {
		if _, err := ResolveImportURL(context.Background(), input, nil, GitHubAPIBaseURL); err == nil {
			t.Fatalf("invalid import source accepted: %s", input)
		}
	}
}
