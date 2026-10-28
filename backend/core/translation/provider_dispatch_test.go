package translation

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"lazymind/core/modelconfig"
)

type dispatchTransport func(*http.Request) (*http.Response, error)

func (transport dispatchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestTranslateWithSelectedKeylessProvider(t *testing.T) {
	client := &http.Client{Transport: dispatchTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "translate.googleapis.com" {
			t.Fatalf("unexpected host: %s", req.URL.Hostname())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[["你好","en"]]`)), Header: make(http.Header)}, nil
	})}
	got, err := translateWithConfig(t.Context(), client, "user-1", modelconfig.TranslationConfig{ProviderName: "Google Translate Web"}, "hello", "zh")
	if err != nil {
		t.Fatal(err)
	}
	if got.TranslatedText != "你好" || got.Source != "en" || got.Target != "zh" {
		t.Fatalf("translation = %#v", got)
	}
}

func TestTranslateWithSelectedSogouWebProvider(t *testing.T) {
	client := &http.Client{Transport: dispatchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Hostname() != "fanyi.sogou.com" {
			t.Fatalf("unexpected Sogou request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<html><script>window.__INITIAL_STATE__={"textTranslate":{"translateData":{"translate":{"from":"en","to":"zh-CHS","dit":"你好世界","errorCode":"0"}}}};</script><p id="trans-result">你好世界</p></html>`)), Header: make(http.Header), Request: req}, nil
	})}
	got, err := translateWithConfig(t.Context(), client, "user-sogou", modelconfig.TranslationConfig{ProviderName: "Sogou Translate Web"}, "Hello world", "zh")
	if err != nil {
		t.Fatal(err)
	}
	if got.TranslatedText != "你好世界" || got.Source != "en" || got.Target != "zh" {
		t.Fatalf("translation = %#v", got)
	}
}

func TestTranslateWithSelectedBingWebProvider(t *testing.T) {
	client := &http.Client{Transport: dispatchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<script>var params_AbusePreventionHelper = [1793163553393,"session-token",3600000]; var _G = {"ig":"ABCDEF0123456789ABCDEF0123456789"};</script><div data-iid="translator.5024"></div>`)), Header: make(http.Header), Request: req}
			return response, nil
		}
		if req.Method != http.MethodPost || req.URL.Hostname() != "www.bing.com" {
			t.Fatalf("unexpected Bing request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[{"translations":[{"text":"你好"}],"detectedLanguage":{"language":"en"}}]`)), Header: make(http.Header), Request: req}, nil
	})}
	got, err := translateWithConfig(t.Context(), client, "user-bing", modelconfig.TranslationConfig{ProviderName: "Bing Translate Web"}, "Hello", "zh")
	if err != nil || got.TranslatedText != "你好" || got.Source != "en" || got.Target != "zh" {
		t.Fatalf("translation = %#v, err = %v", got, err)
	}
}

func TestTranslateWithSelectedDeepLWebProvider(t *testing.T) {
	client := &http.Client{Transport: dispatchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Hostname() != "oneshot-free.www.deepl.com" {
			t.Fatalf("unexpected DeepL web request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"translations":[{"text":"你好","detected_source_language":"en"}]}`)), Header: make(http.Header), Request: req}, nil
	})}
	got, err := translateWithConfig(t.Context(), client, "user-deepl-web", modelconfig.TranslationConfig{ProviderName: "DeepL Web"}, "Hello", "zh")
	if err != nil || got.TranslatedText != "你好" || got.Source != "en" || got.Target != "zh" {
		t.Fatalf("translation = %#v, err = %v", got, err)
	}
}

func TestSplitProviderTextRespectsFreeBaiduLimit(t *testing.T) {
	spec, ok := lookupProvider("Baidu Translation")
	if !ok {
		t.Fatal("missing provider")
	}
	parts, err := splitProviderText(strings.Repeat("文", 1200), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || len([]rune(parts[0])) != 1000 || len([]rune(parts[1])) != 200 {
		t.Fatalf("parts lengths = %d, %d", len([]rune(parts[0])), len([]rune(parts[1])))
	}
}

func TestMyMemoryRejectsOversizedSelection(t *testing.T) {
	_, err := translateWithConfigSource(t.Context(), &http.Client{}, "user-1", modelconfig.TranslationConfig{ProviderName: "MyMemory"}, strings.Repeat("文", 168), "zh", "en")
	if err == nil || !strings.Contains(err.Error(), "500 bytes") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderChunkReassemblyDoesNotInventNewlines(t *testing.T) {
	client := &http.Client{Transport: dispatchTransport(func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		body := `[[` + strconv.Quote(req.Form.Get("q")) + `,"en"]]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	input := strings.Repeat("a", 1001)
	got, err := translateWithConfig(t.Context(), client, "user-reassembly", modelconfig.TranslationConfig{ProviderName: "Google Translate Web"}, input, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if got.TranslatedText != input {
		t.Fatalf("reassembled text differs: len=%d, newline=%v", len(got.TranslatedText), strings.Contains(got.TranslatedText, "\n"))
	}
}

func TestTokenHubChunksStayBelowInputTokenLimit(t *testing.T) {
	spec, ok := lookupProvider("Tencent TokenHub Translation")
	if !ok {
		t.Fatal("missing TokenHub provider")
	}
	parts, err := splitProviderText(strings.Repeat("😀", 5000), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 {
		t.Fatalf("expected multiple TokenHub chunks, got %d", len(parts))
	}
	for _, part := range parts {
		if len([]byte(part)) > 3000 {
			t.Fatalf("TokenHub chunk is %d UTF-8 bytes", len([]byte(part)))
		}
	}
}
