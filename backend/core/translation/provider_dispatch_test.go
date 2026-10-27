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
