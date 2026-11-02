package translation

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSogouTranslateWebRequestsChineseAndParsesResult(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "fanyi.sogou.com" || req.URL.Path != "/text" {
			t.Fatalf("unexpected Sogou request: %s %s", req.Method, req.URL)
		}
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "Mozilla/5.0") || !strings.Contains(got, "Chrome/") {
			t.Fatalf("unexpected Sogou User-Agent: %q", got)
		}
		query := req.URL.Query()
		if query.Get("keyword") != "Hello world" || query.Get("transfrom") != "auto" || query.Get("transto") != "zh-CHS" || query.Get("model") != "general" {
			t.Fatalf("unexpected Sogou query: %s", req.URL.RawQuery)
		}
		return webTestResponse(http.StatusOK, `<html><body><script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"en","to":"zh-CHS","dit":"你好&世界","errorCode":"0"}}}};(function(){})();</script><p class="output-val" id='trans-result'>你好&amp;世界</p></body></html>`), nil
	})
	translated, source, err := callSogouTranslateWeb(context.Background(), client, "Hello world", "zh")
	if err != nil || translated != "你好&世界" || source != "en" {
		t.Fatalf("Sogou result: %q, %q, %v", translated, source, err)
	}
}

func TestSogouTranslateWebUsesStructuredEnglishResult(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("transto"); got != "en" {
			t.Fatalf("target language = %q", got)
		}
		return webTestResponse(http.StatusOK, `<script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"zh-CHS","to":"en","dit":"Hello & goodbye world","errorCode":"0"}}}};</script><p id="trans-result"><span class="trans-sentence">Hello &amp; goodbye </span><span class="trans-sentence">world</span><span class="loading" style="display:none;">Loading...</span></p>`), nil
	})
	translated, source, err := callSogouTranslateWeb(context.Background(), client, "你好世界", "en")
	if err != nil || translated != "Hello & goodbye world" || source != "zh" {
		t.Fatalf("Sogou English result: %q, %q, %v", translated, source, err)
	}
}

func TestSogouTranslateWebRejectsDirectionFlip(t *testing.T) {
	client := webTestClient(t, func(*http.Request) (*http.Response, error) {
		return webTestResponse(http.StatusOK, `<script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"zh-CHS","to":"en","dit":"Hello world","errorCode":"0"}}}};</script><p id="trans-result">Hello world</p>`), nil
	})
	translated, _, err := callSogouTranslateWeb(t.Context(), client, "你好世界", "zh")
	if err == nil || translated != "" {
		t.Fatalf("Sogou silently changed target: translated=%q, error=%v", translated, err)
	}
}

func TestSogouTranslateWebRejectsInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "missing result", status: http.StatusOK, body: `<p id="output-placeholder">cached text</p>`},
		{name: "missing state", status: http.StatusOK, body: `<p id="trans-result">cached text</p>`},
		{name: "empty result", status: http.StatusOK, body: `<script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"en","to":"zh-CHS","dit":"","errorCode":"0"}}}};</script><p id="trans-result">cached text</p>`},
		{name: "service error", status: http.StatusOK, body: `<script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"en","to":"zh-CHS","dit":"cached text","errorCode":"100"}}}};</script><p id="trans-result">cached text</p>`},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `<p id="trans-result">stale text</p>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := webTestClient(t, func(*http.Request) (*http.Response, error) {
				return webTestResponse(test.status, test.body), nil
			})
			if _, _, err := callSogouTranslateWeb(context.Background(), client, "Hello", "zh"); err == nil {
				t.Fatal("expected Sogou translation error")
			}
		})
	}
	client := webTestClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported target must not send a request")
		return nil, nil
	})
	if _, _, err := callSogouTranslateWeb(context.Background(), client, "Hello", "fr"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported target error = %v", err)
	}
}

func TestSogouTranslateWebFollowsSameHostRedirect(t *testing.T) {
	requests := 0
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.URL.Path {
		case "/text":
			response := webTestResponse(http.StatusFound, "")
			response.Header.Set("Location", "https://fanyi.sogou.com/translated")
			return response, nil
		case "/translated":
			return webTestResponse(http.StatusOK, `<script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"en","to":"zh-CHS","dit":"你好世界","errorCode":"0"}}}};</script>`), nil
		default:
			t.Fatalf("unexpected redirect path %q", req.URL.Path)
			return nil, nil
		}
	})
	translated, source, err := callSogouTranslateWeb(context.Background(), client, "Hello world", "zh")
	if err != nil || translated != "你好世界" || source != "en" || requests != 2 {
		t.Fatalf("same-host redirect: translated=%q source=%q requests=%d error=%v", translated, source, requests, err)
	}
}

func TestSogouTranslateWebRejectsCrossOriginRedirect(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "fanyi.sogou.com" {
			t.Fatalf("translation text redirected to %s", req.URL.Host)
		}
		response := webTestResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://example.invalid/collect")
		return response, nil
	})
	if _, _, err := callSogouTranslateWeb(context.Background(), client, "private text", "zh"); err == nil {
		t.Fatal("expected redirect to be rejected")
	}
}

func TestSogouTranslateWebRejectsInsecureAndNondefaultPortRedirects(t *testing.T) {
	for _, destination := range []string{
		"http://fanyi.sogou.com/collect",
		"https://fanyi.sogou.com:444/collect",
	} {
		t.Run(destination, func(t *testing.T) {
			client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/text" {
					t.Fatalf("translation text redirected to %s", req.URL)
				}
				response := webTestResponse(http.StatusFound, "")
				response.Header.Set("Location", destination)
				return response, nil
			})
			if _, _, err := callSogouTranslateWeb(context.Background(), client, "private text", "zh"); err == nil {
				t.Fatal("expected redirect to be rejected")
			}
		})
	}
}

func TestSogouTranslateWebLimitsRedirectHops(t *testing.T) {
	requests := 0
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		response := webTestResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://fanyi.sogou.com/loop")
		return response, nil
	})
	if _, _, err := callSogouTranslateWeb(context.Background(), client, "Hello", "zh"); err == nil || requests < 2 || requests > 5 {
		t.Fatalf("redirect loop: requests=%d, error=%v", requests, err)
	}
}
