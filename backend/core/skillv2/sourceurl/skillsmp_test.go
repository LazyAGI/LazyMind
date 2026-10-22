package sourceurl

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// This fixture preserves the live site's JSON-LD source contract, including a
// display title that differs from the directory containing the selected skill.
const skillsMPFrontendHTML = `<!doctype html><html><head>
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"Organization","url":"https://skillsmp.com"}]}</script>
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"SoftwareSourceCode","name":"Frontend Design","url":"https://skillsmp.com/creators/anthropics/skills/skills-frontend-design","codeRepository":"https://github.com/anthropics/skills/tree/main/skills/frontend-design"},{"@type":"BreadcrumbList","itemListElement":[]}]}</script>
</head><body><a href="https://github.com/unrelated/advertisement">More skills</a></body></html>`

func TestResolveSkillsMPPageURLUsesExactStructuredSource(t *testing.T) {
	for _, path := range []string{
		"/skills/anthropics-skills-skills-frontend-design-skill-md",
		"/creators/anthropics/skills/skills-frontend-design",
		"/zh/creators/anthropics/skills/skills-frontend-design/",
		"/creators/anthropics/skills/skills-frontend-design?utm_source=share#install",
	} {
		t.Run(path, func(t *testing.T) {
			client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != "" {
					t.Error("tracking query was forwarded to the detail page")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				io.WriteString(w, skillsMPFrontendHTML)
			}))
			parsed, _ := url.Parse("https://skillsmp.com" + path)
			got, matched, err := ResolveSkillsMPPageURL(context.Background(), parsed, client)
			if err != nil || !matched {
				t.Fatalf("matched=%v error=%v", matched, err)
			}
			if got.GitHubSourceURL != "https://github.com/anthropics/skills/tree/main/skills/frontend-design" || got.CanonicalName != "frontend-design" {
				t.Fatalf("unexpected source resolution: %#v", got)
			}
		})
	}
}

func TestResolveSkillsMPPageURLValidatesBeforeFetching(t *testing.T) {
	for _, raw := range []string{
		"http://skillsmp.com/skills/example", "https://user:secret@skillsmp.com/skills/example",
		"https://skillsmp.com:443/skills/example", "https://skillsmp.com/skills",
		"https://skillsmp.com/creators/anthropics/skills", "https://skillsmp.com/fr/creators/anthropics/skills",
		"https://skillsmp.com/xx/skills/example", "https://skillsmp.com/skills/one%2Ftwo",
		"https://skillsmp.com/skills/..", "https://skillsmp.com/skills/one/extra",
	} {
		t.Run(raw, func(t *testing.T) {
			parsed, _ := url.Parse(raw)
			client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid page URL was fetched")
			}))
			_, matched, err := ResolveSkillsMPPageURL(context.Background(), parsed, client)
			if !matched || err == nil {
				t.Fatalf("matched=%v error=%v", matched, err)
			}
		})
	}
	for _, raw := range []string{"https://example.com/skills/example", "https://skillsmp.com.evil.test/skills/example"} {
		parsed, _ := url.Parse(raw)
		if _, matched, err := ResolveSkillsMPPageURL(context.Background(), parsed, nil); matched || err != nil {
			t.Fatalf("non-SkillsMP URL matched: %s, %v", raw, err)
		}
	}
	if _, matched, err := ResolveSkillsMPPageURL(context.Background(), nil, nil); matched || err != nil {
		t.Fatalf("nil URL matched: %v", err)
	}
}

func TestResolveSkillsMPPageURLFollowsCanonicalRedirect(t *testing.T) {
	client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/skills/") {
			http.Redirect(w, r, "/creators/anthropics/skills/skills-frontend-design", http.StatusPermanentRedirect)
			return
		}
		io.WriteString(w, skillsMPFrontendHTML)
	}))
	parsed, _ := url.Parse("https://skillsmp.com/skills/old-id")
	got, matched, err := ResolveSkillsMPPageURL(context.Background(), parsed, client)
	if err != nil || !matched || got.GitHubSourceURL != "https://github.com/anthropics/skills/tree/main/skills/frontend-design" {
		t.Fatalf("resolution=%#v matched=%v error=%v", got, matched, err)
	}
}

func TestResolveSkillsMPPageURLRejectsUnsafeRedirects(t *testing.T) {
	for _, target := range []string{"https://evil.test/skills/example", "http://skillsmp.com/skills/example", "https://user:secret@skillsmp.com/skills/example", "https://skillsmp.com:443/skills/example"} {
		t.Run(target, func(t *testing.T) {
			requests := 0
			client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests > 1 {
					t.Error("unsafe redirect was fetched")
					io.WriteString(w, skillsMPFrontendHTML)
					return
				}
				http.Redirect(w, r, target, http.StatusFound)
			}))
			parsed, _ := url.Parse("https://skillsmp.com/skills/example")
			if _, matched, err := ResolveSkillsMPPageURL(context.Background(), parsed, client); !matched || err == nil {
				t.Fatalf("matched=%v error=%v", matched, err)
			}
		})
	}
}

func TestResolveSkillsMPPageURLRejectsMissingOrUnsafeSource(t *testing.T) {
	for _, body := range []string{
		`<a href="https://github.com/anthropics/skills/tree/main/skills/frontend-design">source</a>`,
		`<script type="application/ld+json">{broken</script>`,
		`<script type="application/ld+json">{"@type":"WebSite","codeRepository":"https://github.com/anthropics/skills/tree/main/skills/frontend-design"}</script>`,
		`<script type="application/ld+json">{"@type":"SoftwareSourceCode","codeRepository":"https://evil.test/a/b/tree/main"}</script>`,
		`<script type="application/ld+json">{"@type":"SoftwareSourceCode","codeRepository":"https://github.com/a/b"}</script>`,
		`<script type="application/ld+json">{"@type":"SoftwareSourceCode","codeRepository":"https://user:secret@github.com/a/b/tree/main/skill"}</script>`,
		`<script type="application/ld+json">{"@type":"SoftwareSourceCode","codeRepository":"https://github.com/a/b/tree/main/skill?token=secret"}</script>`,
		`<script type="application/ld+json">{"@graph":[{"@type":"SoftwareSourceCode","codeRepository":"https://github.com/a/b/tree/main/first"},{"@type":"SoftwareSourceCode","codeRepository":"https://github.com/a/b/tree/main/second"}]}</script>`,
	} {
		t.Run(body, func(t *testing.T) {
			client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			parsed, _ := url.Parse("https://skillsmp.com/skills/example")
			if _, matched, err := ResolveSkillsMPPageURL(context.Background(), parsed, client); !matched || err == nil {
				t.Fatalf("matched=%v error=%v", matched, err)
			}
		})
	}
}

func TestResolveSkillsMPPageURLHandlesBlobAndBoundsResponse(t *testing.T) {
	t.Run("SKILL.md source", func(t *testing.T) {
		client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<script TYPE='application/ld+json'>{"@type":["SoftwareSourceCode"],"codeRepository":"https://github.com/example/repo/blob/main/skills/exact-name/SKILL.md"}</script>`)
		}))
		parsed, _ := url.Parse("https://skillsmp.com/skills/opaque-slug")
		got, _, err := ResolveSkillsMPPageURL(context.Background(), parsed, client)
		if err != nil || got.CanonicalName != "exact-name" {
			t.Fatalf("resolution=%#v error=%v", got, err)
		}
	})
	t.Run("oversized HTML", func(t *testing.T) {
		client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, skillsMPFrontendHTML+strings.Repeat(" ", 2<<20))
		}))
		parsed, _ := url.Parse("https://skillsmp.com/skills/example")
		if _, _, err := ResolveSkillsMPPageURL(context.Background(), parsed, client); err == nil {
			t.Fatal("oversized response was accepted")
		}
	})
	t.Run("HTTP error", func(t *testing.T) {
		client := skillsMPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, skillsMPFrontendHTML)
		}))
		parsed, _ := url.Parse("https://skillsmp.com/skills/example")
		if _, _, err := ResolveSkillsMPPageURL(context.Background(), parsed, client); err == nil {
			t.Fatal("HTTP error was accepted")
		}
	})
}

func skillsMPTestClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	return client
}
