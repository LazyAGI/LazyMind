package doc

import (
	"net/url"
	"strings"
	"testing"
)

func TestStaticFileReferencesRequireCoreEndpoint(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://core.example:8443/team/api/core/")
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"relative", "/static-files/figure.png?expires=1&sig=old", "/static-files/figure.png"},
		{"api relative", "/api/core/static-files/figure.png?expires=1&sig=old", "/static-files/figure.png"},
		{"configured endpoint", "https://core.example:8443/team/api/core/static-files/figure.png?expires=1&sig=old", "/static-files/figure.png"},
		{"case insensitive host", "https://CORE.example:8443/team/api/core/static-files/figure.png", "/static-files/figure.png"},
		{"encoded filename", "https://core.example:8443/team/api/core/static-files/figure%20one.png", "/static-files/figure%20one.png"},
		{"external host", "https://cdn.example/static-files/figure.png?token=abc", ""},
		{"host suffix", "https://core.example.evil:8443/team/api/core/static-files/figure.png", ""},
		{"different scheme", "http://core.example:8443/team/api/core/static-files/figure.png", ""},
		{"different port", "https://core.example/team/api/core/static-files/figure.png", ""},
		{"different base path", "https://core.example:8443/other/api/core/static-files/figure.png", ""},
		{"nested path", "https://core.example:8443/team/api/core/other/static-files/figure.png", ""},
		{"userinfo", "https://user@core.example:8443/team/api/core/static-files/figure.png", ""},
		{"protocol relative", "//cdn.example/static-files/figure.png", ""},
		{"unrelated relative path", "/other/static-files/figure.png", ""},
		{"query marker", "https://cdn.example/image?path=/static-files/figure.png", ""},
		{"data URL", "data:image/svg+xml,<svg>/static-files/figure.png</svg>", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StaticFileReferenceFromAnyStoragePath(tc.source); got != tc.want {
				t.Fatalf("reference = %q, want %q", got, tc.want)
			}
			signed := StaticFileURLFromAnyStoragePath(tc.source)
			if tc.want == "" {
				if signed != "" {
					t.Fatalf("non-Core URL was signed: %q", signed)
				}
				return
			}
			u, err := url.Parse(signed)
			if err != nil || u.EscapedPath() != tc.want || u.Query().Get("sig") == "" || u.Query().Get("sig") == "old" {
				t.Fatalf("Core URL was not refreshed: %q (%v)", signed, err)
			}
		})
	}
}

func TestStaticFileReferencesRejectInvalidPublicEndpoint(t *testing.T) {
	for _, base := range []string{
		"//core.example/api/core", "ftp://core.example/api/core",
		"https://user@core.example/api/core", "https://core.example/api/core?query=1",
		"https://core.example/api/core#fragment",
	} {
		t.Run(base, func(t *testing.T) {
			t.Setenv("LAZYMIND_PUBLIC_BASE_URL", base)
			if got := StaticFileReferenceFromAnyStoragePath("https://core.example/api/core/static-files/figure.png"); got != "" {
				t.Fatalf("invalid public endpoint accepted: %q", got)
			}
			if got := StaticFileURLFromAnyStoragePath("/static-files/figure.png"); !strings.HasPrefix(got, "/static-files/figure.png?") {
				t.Fatalf("relative Core reference should still work: %q", got)
			}
		})
	}
}
