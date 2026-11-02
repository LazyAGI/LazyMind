package translation

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDeepLWebTranslatesWithoutCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, input, target, apiTarget, want, source string
	}{
		{name: "English to Chinese", input: "Hello, world.", target: "zh", apiTarget: "zh-Hans", want: "你好，世界。", source: "en"},
		{name: "Chinese to English", input: "你好，世界。", target: "en", apiTarget: "en", want: "Hello, world.", source: "zh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != "https://oneshot-free.www.deepl.com/v1/translate" {
					t.Fatalf("unexpected DeepL web endpoint: %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Authorization") != "None" || req.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected DeepL web headers: %v", req.Header)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if _, exists := body["source_lang"]; exists {
					t.Fatal("source_lang should be omitted to let DeepL detect it")
				}
				var texts []string
				var target string
				if err := json.Unmarshal(body["text"], &texts); err != nil || len(texts) != 1 || texts[0] != tc.input {
					t.Fatalf("unexpected text: %v (%v)", texts, err)
				}
				if err := json.Unmarshal(body["target_lang"], &target); err != nil || target != tc.apiTarget {
					t.Fatalf("unexpected target_lang: %q (%v)", target, err)
				}
				response, _ := json.Marshal(map[string]any{"translations": []map[string]string{{"text": tc.want, "detected_source_language": tc.source}}})
				return webTestResponse(http.StatusOK, string(response)), nil
			})
			got, source, err := callDeepLWeb(t.Context(), client, tc.input, tc.target)
			if err != nil || got != tc.want || source != tc.source {
				t.Fatalf("DeepL web result: %q, %q, %v", got, source, err)
			}
		})
	}
}

func TestDeepLWebRejectsUnsupportedTarget(t *testing.T) {
	if _, _, err := callDeepLWeb(context.Background(), nil, "Hello", "fr"); err == nil {
		t.Fatal("expected unsupported target error")
	}
}

func TestDeepLWebRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"translations":`},
		{name: "missing translations", status: http.StatusOK, body: `{}`},
		{name: "empty translation", status: http.StatusOK, body: `{"translations":[{"text":"  "}]}`},
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat("x", webResponseLimit+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := webTestClient(t, func(*http.Request) (*http.Response, error) {
				return webTestResponse(tc.status, tc.body), nil
			})
			if _, _, err := callDeepLWeb(t.Context(), client, "Hello", "zh"); err == nil {
				t.Fatal("expected DeepL web response error")
			}
		})
	}
}

func TestDeepLWebRejectsRedirect(t *testing.T) {
	requests := 0
	client := webTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if requests > 1 {
			t.Fatalf("followed a DeepL web redirect to %s", req.URL)
		}
		resp := webTestResponse(http.StatusFound, "")
		resp.Header.Set("Location", "https://unexpected.example/translate")
		return resp, nil
	})
	if _, _, err := callDeepLWeb(t.Context(), client, "Hello", "zh"); err == nil || requests != 1 {
		t.Fatalf("expected rejected redirect after one request, got %d requests and %v", requests, err)
	}
}
