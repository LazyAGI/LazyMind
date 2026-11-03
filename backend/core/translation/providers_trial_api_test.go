package translation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/modelconfig"
)

type trialRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn trialRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func trialClient(fn trialRoundTripFunc) *http.Client { return &http.Client{Transport: fn} }

func trialResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestYoudaoTranslation(t *testing.T) {
	client := trialClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://openapi.youdao.com/api" {
			t.Fatalf("unexpected Youdao request: %s %s", req.Method, req.URL)
		}
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		form := req.Form
		if form.Get("q") != "Hello world" || form.Get("from") != "auto" || form.Get("to") != "zh-CHS" || form.Get("signType") != "v3" || form.Get("appKey") != "app-id" {
			t.Fatalf("unexpected Youdao form: %v", form)
		}
		if form.Get("salt") == "" || form.Get("curtime") == "" {
			t.Fatal("missing signature nonce or timestamp")
		}
		sum := sha256.Sum256([]byte("app-id" + "Hello world" + form.Get("salt") + form.Get("curtime") + "app-secret"))
		if form.Get("sign") != hex.EncodeToString(sum[:]) {
			t.Fatal("invalid Youdao v3 signature")
		}
		return trialResponse(http.StatusOK, `{"errorCode":"0","translation":["你好世界"],"l":"en2zh-CHS"}`), nil
	})
	cfg := modelconfig.TranslationConfig{APIKey: "app-id|app-secret"}
	translated, source, err := callYoudaoTranslation(context.Background(), client, cfg, "Hello world", "zh")
	if err != nil || translated != "你好世界" || source != "en" {
		t.Fatalf("Youdao result: %q, %q, %v", translated, source, err)
	}
}

func TestDeepLUsesOnlyOfficialHosts(t *testing.T) {
	client := trialClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "api-free.deepl.com" || req.URL.Path != "/v2/translate" || req.Header.Get("Authorization") != "DeepL-Auth-Key legacy-free-key:fx" {
			t.Fatalf("unexpected DeepL request: %s %s", req.Method, req.URL)
		}
		var payload struct {
			Text       []string `json:"text"`
			TargetLang string   `json:"target_lang"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Text) != 1 || payload.Text[0] != "Hello world" || payload.TargetLang != "ZH" {
			t.Fatalf("unexpected DeepL body: %#v", payload)
		}
		return trialResponse(http.StatusOK, `{"translations":[{"detected_source_language":"EN","text":"你好世界"}]}`), nil
	})
	cfg := modelconfig.TranslationConfig{BaseURL: "https://api.deepl.com/", APIKey: "legacy-free-key:fx"}
	translated, source, err := callDeepL(context.Background(), client, cfg, "Hello world", "zh")
	if err != nil || translated != "你好世界" || source != "en" {
		t.Fatalf("DeepL result: %q, %q, %v", translated, source, err)
	}
	cfg.BaseURL = "https://example.com/"
	if _, _, err := callDeepL(context.Background(), client, cfg, "Hello world", "zh"); err == nil {
		t.Fatal("expected unsupported DeepL host error")
	}
}

func TestDeepLStandardKeyIgnoresStaleFreeCatalogURL(t *testing.T) {
	client := trialClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "api.deepl.com" || req.URL.Path != "/v2/translate" {
			t.Fatalf("unexpected DeepL request: %s %s", req.Method, req.URL)
		}
		return trialResponse(http.StatusOK, `{"translations":[{"detected_source_language":"EN","text":"你好世界"}]}`), nil
	})
	cfg := modelconfig.TranslationConfig{BaseURL: "https://api-free.deepl.com/", APIKey: "standard-key"}
	translated, source, err := callDeepL(context.Background(), client, cfg, "Hello world", "zh")
	if err != nil || translated != "你好世界" || source != "en" {
		t.Fatalf("DeepL result: %q, %q, %v", translated, source, err)
	}
}

func TestTrialRequestDoesNotFollowRedirect(t *testing.T) {
	requests := 0
	client := trialClient(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Host != "api.deepl.com" {
			t.Fatalf("credential-bearing request reached redirect target: %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{"Location": []string{"https://attacker.example/collect"}},
		}, nil
	})
	_, _, err := callDeepL(context.Background(), client, modelconfig.TranslationConfig{APIKey: "standard-key"}, "Hello world", "zh")
	if err == nil {
		t.Fatal("expected redirect to be rejected")
	}
	if requests != 1 {
		t.Fatalf("expected one request to the provider, got %d", requests)
	}
}

func TestCaiyunTranslation(t *testing.T) {
	client := trialClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.interpreter.caiyunai.com/v1/translator" || req.Header.Get("X-Authorization") != "token caiyun-token" {
			t.Fatalf("unexpected Caiyun request: %s %s", req.Method, req.URL)
		}
		var payload struct {
			Source    []string `json:"source"`
			TransType string   `json:"trans_type"`
			Detect    bool     `json:"detect"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Source) != 1 || payload.Source[0] != "Hello world" || payload.TransType != "auto2zh" || !payload.Detect {
			t.Fatalf("unexpected Caiyun body: %#v", payload)
		}
		return trialResponse(http.StatusOK, `{"rc":0,"trans_type":"en2zh","target":["你好，世界"]}`), nil
	})
	cfg := modelconfig.TranslationConfig{APIKey: "caiyun-token"}
	translated, source, err := callCaiyunTranslation(context.Background(), client, cfg, "Hello world", "zh")
	if err != nil || translated != "你好，世界" || source != "en" {
		t.Fatalf("Caiyun result: %q, %q, %v", translated, source, err)
	}
}

func TestTencentTokenHubTranslation(t *testing.T) {
	client := trialClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://tokenhub.tencentmaas.com/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer tokenhub-key" {
			t.Fatalf("unexpected TokenHub request: %s %s", req.Method, req.URL)
		}
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "hy-mt2-lite" || len(payload.Messages) != 2 || payload.Messages[1].Content != "Hello world" {
			t.Fatalf("unexpected TokenHub body: %#v", payload)
		}
		return trialResponse(http.StatusOK, `{"choices":[{"message":{"content":"你好世界"},"finish_reason":"stop"}]}`), nil
	})
	cfg := modelconfig.TranslationConfig{APIKey: "tokenhub-key"}
	translated, source, err := callTencentTokenHubTranslation(context.Background(), client, cfg, "Hello world", "zh")
	if err != nil || translated != "你好世界" || source != "" {
		t.Fatalf("TokenHub result: %q, %q, %v", translated, source, err)
	}
}

func TestTrialTranslationRejectsEmptyResults(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func(*http.Client) error
	}{
		{"Youdao", func(c *http.Client) error {
			_, _, err := callYoudaoTranslation(ctx, c, modelconfig.TranslationConfig{APIKey: "id|secret"}, "Hello", "zh")
			return err
		}},
		{"DeepL", func(c *http.Client) error {
			_, _, err := callDeepL(ctx, c, modelconfig.TranslationConfig{APIKey: "key"}, "Hello", "zh")
			return err
		}},
		{"Caiyun", func(c *http.Client) error {
			_, _, err := callCaiyunTranslation(ctx, c, modelconfig.TranslationConfig{APIKey: "key"}, "Hello", "zh")
			return err
		}},
		{"TokenHub", func(c *http.Client) error {
			_, _, err := callTencentTokenHubTranslation(ctx, c, modelconfig.TranslationConfig{APIKey: "key"}, "Hello", "zh")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := trialClient(func(*http.Request) (*http.Response, error) { return trialResponse(http.StatusOK, `{}`), nil })
			if err := tc.call(client); err == nil {
				t.Fatal("expected empty response error")
			}
		})
	}
}
