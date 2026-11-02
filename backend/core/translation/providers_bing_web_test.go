package translation

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestBingTranslateWebFollowsRegionalRedirectAndKeepsSession(t *testing.T) {
	const page = `<script>var params_AbusePreventionHelper = [1793163553393,"session-token",3600000]; var _G = {"ig":"ABCDEF0123456789ABCDEF0123456789"};</script><div data-iid="translator.5023"></div><div data-iid="translator.5024"></div>`
	requests := 0
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if req.Method != http.MethodGet || req.URL.String() != "https://www.bing.com/translator" {
				t.Fatalf("initial Bing request = %s %s", req.Method, req.URL)
			}
			resp := webTestResponse(http.StatusFound, "")
			resp.Header.Set("Location", "https://cn.bing.com/translator")
			resp.Request = req
			return resp, nil
		case 2:
			if req.Method != http.MethodGet || req.URL.String() != "https://cn.bing.com/translator" {
				t.Fatalf("redirected Bing request = %s %s", req.Method, req.URL)
			}
			resp := webTestResponse(http.StatusOK, page)
			resp.Header.Add("Set-Cookie", "session=regional; Path=/; Secure")
			resp.Request = req
			return resp, nil
		case 3:
			if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "cn.bing.com" || req.URL.Path != "/ttranslatev3" {
				t.Fatalf("Bing translation request = %s %s", req.Method, req.URL)
			}
			if query := req.URL.Query(); query.Get("IG") != "ABCDEF0123456789ABCDEF0123456789" || query.Get("IID") != "translator.5024" || query.Get("isVertical") != "1" {
				t.Fatalf("Bing translation query = %s", req.URL.RawQuery)
			}
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			for field, want := range map[string]string{"fromLang": "auto-detect", "to": "zh-Hans", "text": "Hello world", "token": "session-token", "key": "1793163553393"} {
				if got := req.Form.Get(field); got != want {
					t.Errorf("Bing form %s = %q, want %q", field, got, want)
				}
			}
			if cookie, err := req.Cookie("session"); err != nil || cookie.Value != "regional" {
				t.Errorf("Bing session cookie = %v, %v", cookie, err)
			}
			return webTestResponse(http.StatusOK, `[{"translations":[{"text":"你好，世界","to":"zh-Hans"}],"usedLLM":true,"detectedLanguage":{"language":"en"}}]`), nil
		default:
			t.Fatalf("unexpected Bing request %d: %s", requests, req.URL)
			return nil, nil
		}
	})
	translated, source, err := callBingTranslateWeb(context.Background(), client, "Hello world", "zh")
	if err != nil || translated != "你好，世界" || source != "en" || requests != 3 {
		t.Fatalf("Bing translation = %q, %q, %v; requests = %d", translated, source, err, requests)
	}
}

func TestBingTranslateWebRejectsResponseWithoutFinalURL(t *testing.T) {
	client := webTestClient(t, func(*http.Request) (*http.Response, error) {
		return webTestResponse(http.StatusOK, `<html></html>`), nil
	})
	if _, _, err := callBingTranslateWeb(context.Background(), client, "Hello", "zh"); err == nil {
		t.Fatal("expected an error when the translator page has no final URL")
	}
}

func TestBingTranslateWebDoesNotFollowPageRedirectOutsideBing(t *testing.T) {
	requests := 0
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			resp := webTestResponse(http.StatusFound, "")
			resp.Header.Set("Location", "http://127.0.0.1:8080/collect")
			resp.Request = req
			return resp, nil
		}
		resp := webTestResponse(http.StatusOK, `<html></html>`)
		resp.Request = req
		return resp, nil
	})
	if _, _, err := callBingTranslateWeb(t.Context(), client, "private text", "zh"); err == nil || requests != 1 {
		t.Fatalf("page redirect requests=%d, error=%v; expected rejection before redirected request", requests, err)
	}
}

func TestBingTranslateWebReportsEmbeddedServiceFailure(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			resp := webTestResponse(http.StatusOK, `<script>var params_AbusePreventionHelper = [1793163553393,"session-token",3600000]; var _G = {"ig":"ABCDEF0123456789ABCDEF0123456789"};</script><div data-iid="translator.5024"></div>`)
			resp.Request = req
			return resp, nil
		}
		return webTestResponse(http.StatusOK, `{"statusCode":429,"message":"Too many requests"}`), nil
	})
	translated, _, err := callBingTranslateWeb(context.Background(), client, "Hello", "zh")
	if translated != "" || err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("Bing service failure = %q, %v; want status 429", translated, err)
	}
}

func TestBingTranslateWebDoesNotForwardSessionOnPOSTRedirect(t *testing.T) {
	requests := 0
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method == http.MethodGet {
			resp := webTestResponse(http.StatusOK, `<script>var params_AbusePreventionHelper = [1793163553393,"session-token",3600000]; var _G = {"ig":"ABCDEF0123456789ABCDEF0123456789"};</script><div data-iid="translator.5024"></div>`)
			resp.Request = req
			return resp, nil
		}
		if req.URL.Host != "www.bing.com" {
			return webTestResponse(http.StatusOK, `[{"translations":[{"text":"leaked"}]}]`), nil
		}
		resp := webTestResponse(http.StatusTemporaryRedirect, "")
		resp.Header.Set("Location", "https://other.example/collect")
		return resp, nil
	})
	translated, _, err := callBingTranslateWeb(context.Background(), client, "private text", "zh")
	if err == nil || translated != "" || requests != 2 {
		t.Fatalf("Bing POST redirect = %q, %v; requests = %d, want rejection after 2 requests", translated, err, requests)
	}
}
