package translation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type webRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn webRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func webTestClient(t *testing.T, fn webRoundTripFunc) *http.Client {
	t.Helper()
	return &http.Client{Transport: fn}
}

func webTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestGoogleTranslateWeb(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "translate.googleapis.com" || req.URL.Path != "/translate_a/t" {
			t.Fatalf("unexpected Google request: %s %s", req.Method, req.URL)
		}
		if req.URL.Query().Get("client") != "gtx" || req.URL.Query().Get("sl") != "auto" || req.URL.Query().Get("tl") != "zh" {
			t.Fatalf("unexpected Google query: %s", req.URL.RawQuery)
		}
		if err := req.ParseForm(); err != nil || req.Form.Get("q") != "Hello world" {
			t.Fatalf("unexpected Google body: %v, %v", req.Form, err)
		}
		return webTestResponse(http.StatusOK, `[["你好世界","en"]]`), nil
	})
	translated, source, err := callGoogleTranslateWeb(context.Background(), client, "Hello world", "zh")
	if err != nil || translated != "你好世界" || source != "en" {
		t.Fatalf("Google result: %q, %q, %v", translated, source, err)
	}
}

func TestGoogleTranslateWebRejectsEmptyResponse(t *testing.T) {
	client := webTestClient(t, func(*http.Request) (*http.Response, error) { return webTestResponse(http.StatusOK, `[]`), nil })
	if _, _, err := callGoogleTranslateWeb(context.Background(), client, "Hello", "zh"); err == nil {
		t.Fatal("expected invalid Google response error")
	}
}

func TestTencentTranSmartWeb(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "transmart.qq.com" || req.URL.Path != "/api/imt" {
			t.Fatalf("unexpected TranSmart request: %s %s", req.Method, req.URL)
		}
		var payload struct {
			Header struct {
				Fn        string `json:"fn"`
				ClientKey string `json:"client_key"`
			} `json:"header"`
			Source struct {
				Lang     string   `json:"lang"`
				TextList []string `json:"text_list"`
			} `json:"source"`
			Target struct {
				Lang string `json:"lang"`
			} `json:"target"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Header.Fn != "auto_translation" || payload.Header.ClientKey == "" || payload.Source.Lang != "auto" || len(payload.Source.TextList) != 1 || payload.Source.TextList[0] != "Hello world" || payload.Target.Lang != "zh" {
			t.Fatalf("unexpected TranSmart payload: %#v", payload)
		}
		return webTestResponse(http.StatusOK, `{"header":{"ret_code":"succ"},"auto_translation":["您好，世界"],"src_lang":"en"}`), nil
	})
	translated, source, err := callTencentTranSmartWeb(context.Background(), client, "Hello world", "zh")
	if err != nil || translated != "您好，世界" || source != "en" {
		t.Fatalf("TranSmart result: %q, %q, %v", translated, source, err)
	}
}

func TestTencentTranSmartWebRejectsFailure(t *testing.T) {
	client := webTestClient(t, func(*http.Request) (*http.Response, error) {
		return webTestResponse(http.StatusOK, `{"header":{"ret_code":"rate_limit"},"auto_translation":[]}`), nil
	})
	if _, _, err := callTencentTranSmartWeb(context.Background(), client, "Hello", "zh"); err == nil {
		t.Fatal("expected TranSmart service error")
	}
}

func TestMyMemory(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "api.mymemory.translated.net" || req.URL.Path != "/get" {
			t.Fatalf("unexpected MyMemory request: %s %s", req.Method, req.URL)
		}
		if req.URL.Query().Get("q") != "Hello world" || req.URL.Query().Get("langpair") != "en|zh-CN" {
			t.Fatalf("unexpected MyMemory query: %s", req.URL.RawQuery)
		}
		return webTestResponse(http.StatusOK, `{"responseStatus":200,"responseData":{"translatedText":"你好世界"},"quotaFinished":false}`), nil
	})
	translated, source, err := callMyMemory(context.Background(), client, "Hello world", "en", "zh")
	if err != nil || translated != "你好世界" || source != "en" {
		t.Fatalf("MyMemory result: %q, %q, %v", translated, source, err)
	}
}

func TestMyMemoryRejectsOversizeAndQuota(t *testing.T) {
	client := webTestClient(t, func(*http.Request) (*http.Response, error) {
		return webTestResponse(http.StatusOK, `{"responseStatus":200,"responseData":{"translatedText":"你好"},"quotaFinished":true}`), nil
	})
	if _, _, err := callMyMemory(context.Background(), client, strings.Repeat("中", 167), "zh", "en"); err == nil {
		t.Fatal("expected 500-byte input limit")
	}
	if _, _, err := callMyMemory(context.Background(), client, "Hello", "en", "zh"); err == nil {
		t.Fatal("expected quota error")
	}
}

func TestMyMemoryRequiresExplicitSource(t *testing.T) {
	for _, source := range []string{"", "fr", "zh"} {
		if err := validateMyMemoryLanguagePair(source, "zh"); err == nil {
			t.Fatalf("source %q unexpectedly accepted", source)
		}
	}
	if err := validateMyMemoryLanguagePair("zh", "en"); err != nil {
		t.Fatal(err)
	}
}

func TestMyMemoryNormalizesExplicitSource(t *testing.T) {
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("langpair"); got != "zh-CN|en" {
			t.Fatalf("langpair = %q", got)
		}
		return webTestResponse(http.StatusOK, `{"responseStatus":200,"responseData":{"translatedText":"hello"}}`), nil
	})
	_, source, err := callMyMemory(t.Context(), client, "你好", " ZH ", "EN")
	if err != nil || source != "zh" {
		t.Fatalf("source = %q, error = %v", source, err)
	}
}
