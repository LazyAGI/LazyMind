package translation

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/modelconfig"
)

type freeAPIRoundTrip func(*http.Request) (*http.Response, error)

func (f freeAPIRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func freeAPIClient(t *testing.T, check func(*http.Request)) *http.Client {
	t.Helper()
	return &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		check(r)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
}

func freeAPIResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestBaiduTranslationSignsOfficialRequestAndReadsTranslation(t *testing.T) {
	called := false
	client := &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "fanyi-api.baidu.com" || r.URL.Path != "/api/trans/vip/translate" {
			t.Errorf("wrong Baidu endpoint: %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		form := r.PostForm
		if form.Get("q") != "Hello" || form.Get("from") != "auto" || form.Get("to") != "zh" || form.Get("appid") != "app-id" || form.Get("salt") == "" {
			t.Errorf("wrong Baidu fields: %v", form)
		}
		want := md5.Sum([]byte("app-id" + "Hello" + form.Get("salt") + "app-secret"))
		if form.Get("sign") != hex.EncodeToString(want[:]) {
			t.Errorf("wrong Baidu signature")
		}
		return freeAPIResponse(`{"from":"en","to":"zh","trans_result":[{"src":"Hello","dst":"你好"}]}`, 200), nil
	})}
	got, source, err := callBaiduTranslation(context.Background(), client, modelconfig.TranslationConfig{APIKey: "app-id|app-secret", BaseURL: "https://fanyi-api.baidu.com"}, "Hello", "zh")
	if err != nil || got != "你好" || source != "en" || !called {
		t.Fatalf("Baidu result: %q %q %v called=%v", got, source, err, called)
	}
}

func TestAliyunTranslationSignsV3FormRequestAndReadsWrappedResponse(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "mt.cn-hangzhou.aliyuncs.com" || r.URL.Path != "/" {
			t.Errorf("wrong Alibaba endpoint: %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for k, want := range map[string]string{"FormatType": "text", "Scene": "general", "SourceLanguage": "auto", "SourceText": "Hello", "TargetLanguage": "zh"} {
			if r.PostForm.Get(k) != want {
				t.Errorf("Alibaba %s=%q, want %q", k, r.PostForm.Get(k), want)
			}
		}
		if r.Header.Get("X-Acs-Action") != "TranslateGeneral" || r.Header.Get("X-Acs-Version") != "2018-10-12" || r.Header.Get("X-Acs-Date") == "" || r.Header.Get("X-Acs-Signature-Nonce") == "" || r.Header.Get("X-Acs-Content-Sha256") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "ACS3-HMAC-SHA256 Credential=ak-id,") {
			t.Errorf("Alibaba request missing V3 signing fields")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Alibaba JSON response was not requested")
		}
		return freeAPIResponse(`{"TranslateGeneralResponse":{"Code":200,"Data":{"Translated":"你好","DetectedLanguage":"en"},"Message":"success"}}`, 200), nil
	})}
	got, source, err := callAliyunTranslation(context.Background(), client, modelconfig.TranslationConfig{APIKey: "ak-id|ak-secret", BaseURL: "https://mt.cn-hangzhou.aliyuncs.com"}, "Hello", "zh")
	if err != nil || got != "你好" || source != "en" {
		t.Fatalf("Alibaba result: %q %q %v", got, source, err)
	}
}

func TestAliyunTranslationReportsStringGatewayErrorCode(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(*http.Request) (*http.Response, error) {
		return freeAPIResponse(`{"Code":"InvalidAccessKeyId.NotFound","Message":"The AccessKey ID does not exist"}`, http.StatusForbidden), nil
	})}
	_, _, err := callAliyunTranslation(context.Background(), client, modelconfig.TranslationConfig{APIKey: "ak-id|ak-secret"}, "Hello", "zh")
	if err == nil || !strings.Contains(err.Error(), "InvalidAccessKeyId.NotFound") {
		t.Fatalf("missing Alibaba error code: %v", err)
	}
}

func TestVolcengineTranslationSignsOfficialRequestAndReadsTranslation(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "translate.volcengineapi.com" || r.URL.Query().Get("Action") != "TranslateText" || r.URL.Query().Get("Version") != "2020-06-01" {
			t.Errorf("wrong Volcengine endpoint: %s %s", r.Method, r.URL)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential=ak-id/") || r.Header.Get("X-Date") == "" || r.Header.Get("X-Content-Sha256") == "" {
			t.Errorf("missing Volcengine signature")
		}
		var body struct {
			TargetLanguage string
			TextList       []string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.TargetLanguage != "zh" || len(body.TextList) != 1 || body.TextList[0] != "Hello" {
			t.Errorf("wrong Volcengine body: %+v", body)
		}
		return freeAPIResponse(`{"TranslationList":[{"Translation":"你好","DetectedSourceLanguage":"en"}],"ResponseMetadata":{"Error":null}}`, 200), nil
	})}
	got, source, err := callVolcengineTranslation(context.Background(), client, modelconfig.TranslationConfig{APIKey: "ak-id|ak-secret", BaseURL: "https://translate.volcengineapi.com"}, "Hello", "zh")
	if err != nil || got != "你好" || source != "en" {
		t.Fatalf("Volcengine result: %q %q %v", got, source, err)
	}
}

func TestMicrosoftTranslatorUsesRegionalKeyAndChineseLanguageCode(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "api.cognitive.microsofttranslator.com" || r.URL.Path != "/translate" || r.URL.Query().Get("api-version") != "3.0" || r.URL.Query().Get("to") != "zh-Hans" {
			t.Errorf("wrong Microsoft endpoint: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Ocp-Apim-Subscription-Key") != "azure-key" || r.Header.Get("Ocp-Apim-Subscription-Region") != "eastus" {
			t.Errorf("wrong Microsoft credentials")
		}
		var body []struct{ Text string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body[0].Text != "Hello" {
			t.Errorf("wrong Microsoft body: %+v", body)
		}
		return freeAPIResponse(`[{"detectedLanguage":{"language":"en","score":1.0},"translations":[{"text":"你好","to":"zh-Hans"}]}]`, 200), nil
	})}
	got, source, err := callMicrosoftTranslator(context.Background(), client, modelconfig.TranslationConfig{APIKey: "azure-key|eastus", BaseURL: "https://api.cognitive.microsofttranslator.com"}, "Hello", "zh")
	if err != nil || got != "你好" || source != "en" {
		t.Fatalf("Microsoft result: %q %q %v", got, source, err)
	}
}

func TestGoogleCloudTranslationUsesBasicAPIKeyAndPlainText(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "translation.googleapis.com" || r.URL.Path != "/language/translate/v2" || r.URL.Query().Get("key") != "google-key" {
			t.Errorf("wrong Google endpoint: %s %s", r.Method, r.URL)
		}
		var body struct {
			Q      string `json:"q"`
			Target string `json:"target"`
			Format string `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Q != "Hello" || body.Target != "zh" || body.Format != "text" {
			t.Errorf("wrong Google body: %+v", body)
		}
		return freeAPIResponse(`{"data":{"translations":[{"translatedText":"你好","detectedSourceLanguage":"en"}]}}`, 200), nil
	})}
	got, source, err := callGoogleCloudTranslation(context.Background(), client, modelconfig.TranslationConfig{APIKey: "google-key", BaseURL: "https://translation.googleapis.com"}, "Hello", "zh")
	if err != nil || got != "你好" || source != "en" {
		t.Fatalf("Google result: %q %q %v", got, source, err)
	}
}

func TestNiutransTranslationPostsKeyAndReadsTranslation(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "api.niutrans.com" || r.URL.Path != "/NiuTransServer/translation" {
			t.Errorf("wrong Niutrans endpoint: %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for k, want := range map[string]string{"apikey": "niu-key", "from": "auto", "to": "zh", "src_text": "Hello"} {
			if r.PostForm.Get(k) != want {
				t.Errorf("Niutrans %s=%q, want %q", k, r.PostForm.Get(k), want)
			}
		}
		return freeAPIResponse(`{"tgt_text":"你好","from":"en","to":"zh"}`, 200), nil
	})}
	got, source, err := callNiutrans(context.Background(), client, modelconfig.TranslationConfig{APIKey: "niu-key", BaseURL: "https://api.niutrans.com"}, "Hello", "zh")
	if err != nil || got != "你好" || source != "en" {
		t.Fatalf("Niutrans result: %q %q %v", got, source, err)
	}
}

func TestFreeAPIAdaptersRejectUntrustedBaseURLBeforeNetwork(t *testing.T) {
	client := freeAPIClient(t, func(*http.Request) { t.Fatal("untrusted URL was requested") })
	cases := []struct {
		name string
		call func(context.Context, *http.Client, modelconfig.TranslationConfig, string, string) (string, string, error)
		key  string
	}{
		{"Baidu", callBaiduTranslation, "id|secret"}, {"Alibaba", callAliyunTranslation, "id|secret"}, {"Volcengine", callVolcengineTranslation, "id|secret"}, {"Microsoft", callMicrosoftTranslator, "key|region"}, {"Google", callGoogleCloudTranslation, "key"}, {"Niutrans", callNiutrans, "key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := c.call(context.Background(), client, modelconfig.TranslationConfig{APIKey: c.key, BaseURL: "https://evil.example/translate"}, "Hello", "zh")
			if err == nil {
				t.Fatal("expected untrusted endpoint error")
			}
		})
	}
}

func TestGoogleCloudTranslationDoesNotExposeKeyOnNetworkError(t *testing.T) {
	client := &http.Client{Transport: freeAPIRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}
	_, _, err := callGoogleCloudTranslation(context.Background(), client, modelconfig.TranslationConfig{APIKey: "sensitive-google-key"}, "Hello", "zh")
	if err == nil || strings.Contains(err.Error(), "sensitive-google-key") {
		t.Fatalf("network error exposed API key: %v", err)
	}
}
