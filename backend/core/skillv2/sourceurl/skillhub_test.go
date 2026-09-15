package sourceurl

import (
	"net/url"
	"testing"
)

func TestResolveSkillHubPageURL(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		wantMatched  bool
		wantErr      bool
		wantCoord    string
		wantDownload string
	}{
		{
			name:         "namespaced skill page",
			rawURL:       "https://skillhub.cn/skills/clawhub_paudyyin/summarize",
			wantMatched:  true,
			wantCoord:    "@clawhub_paudyyin/summarize",
			wantDownload: "https://api.skillhub.cn/api/v1/download?slug=%40clawhub_paudyyin%2Fsummarize",
		},
		{
			name:         "unnamespaced skill page",
			rawURL:       "https://skillhub.cn/skills/summarize",
			wantMatched:  true,
			wantCoord:    "summarize",
			wantDownload: "https://api.skillhub.cn/api/v1/download?slug=summarize",
		},
		{
			name:         "www host",
			rawURL:       "https://www.skillhub.cn/skills/summarize",
			wantMatched:  true,
			wantCoord:    "summarize",
			wantDownload: "https://api.skillhub.cn/api/v1/download?slug=summarize",
		},
		{
			name:        "other host",
			rawURL:      "https://example.test/skills/summarize",
			wantMatched: false,
		},
		{
			name:        "missing slug",
			rawURL:      "https://skillhub.cn/skills",
			wantMatched: true,
			wantErr:     true,
		},
		{
			name:        "query is rejected",
			rawURL:      "https://skillhub.cn/skills/summarize?download=1",
			wantMatched: true,
			wantErr:     true,
		},
		{
			name:        "encoded separator is rejected",
			rawURL:      "https://skillhub.cn/skills/clawhub%2Fescape/summarize",
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
			got, matched, err := ResolveSkillHubPageURL(parsed)
			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveSkillHubPageURL returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSkillHubPageURL returned error: %v", err)
			}
			if got.Coordinate != tt.wantCoord || got.DownloadURL != tt.wantDownload {
				t.Fatalf("resolution = %#v, want coordinate %q and download URL %q", got, tt.wantCoord, tt.wantDownload)
			}
		})
	}
}
