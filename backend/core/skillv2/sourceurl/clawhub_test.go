package sourceurl

import (
	"net/url"
	"testing"
)

func TestResolveClawHubPageURL(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		wantMatched  bool
		wantErr      bool
		wantCoord    string
		wantDownload string
	}{
		{
			name:         "canonical skill page preserves its publisher",
			rawURL:       "https://clawhub.ai/paudyyin/skills/summarize",
			wantMatched:  true,
			wantCoord:    "@paudyyin/summarize",
			wantDownload: "https://clawhub.ai/api/v1/download?owner=paudyyin&slug=summarize",
		},
		{
			name:         "legacy skill page",
			rawURL:       "https://clawhub.ai/pskoett/self-improving-agent",
			wantMatched:  true,
			wantCoord:    "@pskoett/self-improving-agent",
			wantDownload: "https://clawhub.ai/api/v1/download?owner=pskoett&slug=self-improving-agent",
		},
		{
			name:         "query and fragment do not override publisher or skill",
			rawURL:       "https://www.clawhub.ai/paudyyin/skills/summarize/?utm_source=share&owner=someone&slug=other#versions",
			wantMatched:  true,
			wantCoord:    "@paudyyin/summarize",
			wantDownload: "https://clawhub.ai/api/v1/download?owner=paudyyin&slug=summarize",
		},
		{
			name:         "explicit version is preserved",
			rawURL:       "https://clawhub.ai/paudyyin/skills/summarize?version=1.0.0&utm_source=share#versions",
			wantMatched:  true,
			wantCoord:    "@paudyyin/summarize",
			wantDownload: "https://clawhub.ai/api/v1/download?owner=paudyyin&slug=summarize&version=1.0.0",
		},
		{
			name:         "prerelease and build version are preserved",
			rawURL:       "https://clawhub.ai/owner/skills/demo?version=1.2.3-beta.1%2Bbuild.5",
			wantMatched:  true,
			wantCoord:    "@owner/demo",
			wantDownload: "https://clawhub.ai/api/v1/download?owner=owner&slug=demo&version=1.2.3-beta.1%2Bbuild.5",
		},
		{
			name:         "explicit tag is preserved",
			rawURL:       "https://clawhub.ai/paudyyin/skills/summarize?tag=latest",
			wantMatched:  true,
			wantCoord:    "@paudyyin/summarize",
			wantDownload: "https://clawhub.ai/api/v1/download?owner=paudyyin&slug=summarize&tag=latest",
		},
		{name: "other host", rawURL: "https://example.test/paudyyin/summarize"},
		{name: "subdomain spoof", rawURL: "https://clawhub.ai.example.test/paudyyin/summarize"},
		{name: "homepage", rawURL: "https://clawhub.ai", wantMatched: true, wantErr: true},
		{name: "browse page", rawURL: "https://clawhub.ai/skills", wantMatched: true, wantErr: true},
		{name: "missing slug", rawURL: "https://clawhub.ai/paudyyin/skills/", wantMatched: true, wantErr: true},
		{name: "plugin page", rawURL: "https://clawhub.ai/openclaw/plugins/discord", wantMatched: true, wantErr: true},
		{name: "skill subpage", rawURL: "https://clawhub.ai/paudyyin/skills/summarize/settings", wantMatched: true, wantErr: true},
		{name: "download endpoint stays direct", rawURL: "https://clawhub.ai/api/v1/download?owner=paudyyin&slug=summarize"},
		{name: "legacy download endpoint stays direct", rawURL: "https://clawhub.ai/api/download?slug=summarize"},
		{name: "credentials", rawURL: "https://user@clawhub.ai/paudyyin/summarize", wantMatched: true, wantErr: true},
		{name: "port", rawURL: "https://clawhub.ai:443/paudyyin/summarize", wantMatched: true, wantErr: true},
		{name: "extra leading slash", rawURL: "https://clawhub.ai//paudyyin/summarize", wantMatched: true, wantErr: true},
		{name: "extra trailing slash", rawURL: "https://clawhub.ai/paudyyin/summarize//", wantMatched: true, wantErr: true},
		{name: "encoded separator", rawURL: "https://clawhub.ai/paudyyin%2Fother/summarize", wantMatched: true, wantErr: true},
		{name: "encoded traversal", rawURL: "https://clawhub.ai/paudyyin/skills/%2e%2e", wantMatched: true, wantErr: true},
		{name: "encoded whitespace", rawURL: "https://clawhub.ai/paudyyin/summarize%20", wantMatched: true, wantErr: true},
		{name: "query injection in slug", rawURL: "https://clawhub.ai/paudyyin/summarize%26owner%3Dother", wantMatched: true, wantErr: true},
		{name: "empty version", rawURL: "https://clawhub.ai/paudyyin/summarize?version=", wantMatched: true, wantErr: true},
		{name: "duplicate version", rawURL: "https://clawhub.ai/paudyyin/summarize?version=1.0.0&version=2.0.0", wantMatched: true, wantErr: true},
		{name: "conflicting version and tag", rawURL: "https://clawhub.ai/paudyyin/summarize?version=1.0.0&tag=latest", wantMatched: true, wantErr: true},
		{name: "version query injection", rawURL: "https://clawhub.ai/paudyyin/summarize?version=1.0.0%26owner%3Dother", wantMatched: true, wantErr: true},
		{name: "version path injection", rawURL: "https://clawhub.ai/paudyyin/summarize?version=..%2Fother", wantMatched: true, wantErr: true},
		{name: "malformed query", rawURL: "https://clawhub.ai/paudyyin/summarize?version=%zz", wantMatched: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			got, matched, err := ResolveClawHubPageURL(parsed)
			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveClawHubPageURL returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveClawHubPageURL returned error: %v", err)
			}
			if got.Coordinate != tt.wantCoord || got.DownloadURL != tt.wantDownload {
				t.Fatalf("resolution = %#v, want coordinate %q and download URL %q", got, tt.wantCoord, tt.wantDownload)
			}
		})
	}
	if got, matched, err := ResolveClawHubPageURL(nil); err != nil || matched || got.Coordinate != "" || got.DownloadURL != "" {
		t.Fatalf("nil URL resolution = %#v, %v, %v", got, matched, err)
	}
}
