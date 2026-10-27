package translation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lazymind/core/modelconfig"
)

const (
	youdaoTranslationURL = "https://openapi.youdao.com/api"
	caiyunTranslationURL = "https://api.interpreter.caiyunai.com/v1/translator"
)

func trialJSONRequest(ctx context.Context, client *http.Client, endpoint string, payload []byte, headers http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	safeClient := *webTranslationClient(client)
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := safeClient.Do(req)
	if err != nil {
		return nil, err
	}
	return webTranslationBody(resp)
}

func trialTarget(target string) error {
	if target != "zh" && target != "en" {
		return errors.New("unsupported translation target")
	}
	return nil
}

func trialHost(baseURL, defaultHost string, allowed ...string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return defaultHost, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid translation service endpoint")
	}
	for _, host := range allowed {
		if parsed.Host == host {
			return host, nil
		}
	}
	return "", errors.New("invalid translation service endpoint")
}

func youdaoSignInput(text string) string {
	runes := []rune(text)
	if len(runes) <= 20 {
		return text
	}
	return string(runes[:10]) + strconv.Itoa(len(runes)) + string(runes[len(runes)-10:])
}

func languageFromDirection(direction string) string {
	source, _, found := strings.Cut(direction, "2")
	if !found {
		return ""
	}
	if strings.EqualFold(source, "zh-CHS") || strings.EqualFold(source, "zh-CN") {
		return "zh"
	}
	return strings.ToLower(source)
}

// Youdao text translation uses the v3 SHA-256 signature documented at
// https://ai.youdao.com/DOCSIRMA/html/trans/api/wbfy/index.html.
func callYoudaoTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	if err := trialTarget(target); err != nil {
		return "", "", err
	}
	appID, secret, ok := strings.Cut(strings.TrimSpace(cfg.APIKey), "|")
	appID, secret = strings.TrimSpace(appID), strings.TrimSpace(secret)
	if !ok || appID == "" || secret == "" {
		return "", "", errors.New("Youdao application ID and secret are required")
	}
	salt := strconv.FormatInt(time.Now().UnixNano(), 10)
	curtime := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256([]byte(appID + youdaoSignInput(text) + salt + curtime + secret))
	to := target
	if target == "zh" {
		to = "zh-CHS"
	}
	form := url.Values{
		"q":        {text},
		"from":     {"auto"},
		"to":       {to},
		"appKey":   {appID},
		"salt":     {salt},
		"sign":     {hex.EncodeToString(sum[:])},
		"signType": {"v3"},
		"curtime":  {curtime},
	}
	encoded := []byte(form.Encode())
	body, err := trialJSONRequest(ctx, client, youdaoTranslationURL, encoded, http.Header{
		"Content-Type": {"application/x-www-form-urlencoded;charset=utf-8"},
	})
	if err != nil {
		return "", "", fmt.Errorf("Youdao translation request failed: %w", err)
	}
	var result struct {
		ErrorCode   string   `json:"errorCode"`
		Translation []string `json:"translation"`
		Direction   string   `json:"l"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.ErrorCode != "0" || len(result.Translation) == 0 || strings.TrimSpace(result.Translation[0]) == "" {
		return "", "", fmt.Errorf("Youdao translation failed: code %s", result.ErrorCode)
	}
	return result.Translation[0], languageFromDirection(result.Direction), nil
}

// DeepL exposes both its legacy API Free and standard API hosts. See
// https://developers.deepl.com/api-reference/translate/request-translation.
func callDeepL(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	if err := trialTarget(target); err != nil {
		return "", "", err
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return "", "", errors.New("DeepL API key is required")
	}
	if _, err := trialHost(cfg.BaseURL, "api.deepl.com", "api.deepl.com", "api-free.deepl.com"); err != nil {
		return "", "", err
	}
	host := "api.deepl.com"
	if strings.HasSuffix(key, ":fx") {
		host = "api-free.deepl.com"
	}
	encoded, _ := json.Marshal(map[string]any{"text": []string{text}, "target_lang": strings.ToUpper(target)})
	body, err := trialJSONRequest(ctx, client, "https://"+host+"/v2/translate", encoded, http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"DeepL-Auth-Key " + key},
	})
	if err != nil {
		return "", "", fmt.Errorf("DeepL translation request failed: %w", err)
	}
	var result struct {
		Translations []struct {
			Text     string `json:"text"`
			Detected string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Translations) != 1 || strings.TrimSpace(result.Translations[0].Text) == "" {
		return "", "", errors.New("DeepL translation returned an invalid response")
	}
	return result.Translations[0].Text, strings.ToLower(result.Translations[0].Detected), nil
}

// Caiyun's public API and limited trial allowance are documented at
// https://docs.caiyunapp.com/lingocloud-api/index.html.
func callCaiyunTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	if err := trialTarget(target); err != nil {
		return "", "", err
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return "", "", errors.New("Caiyun token is required")
	}
	direction := "auto2zh"
	detect := true
	if target == "en" {
		direction, detect = "zh2en", false
	}
	encoded, _ := json.Marshal(map[string]any{
		"source": []string{text}, "trans_type": direction, "detect": detect, "media": "text",
	})
	body, err := trialJSONRequest(ctx, client, caiyunTranslationURL, encoded, http.Header{
		"Content-Type":    {"application/json"},
		"X-Authorization": {"token " + key},
	})
	if err != nil {
		return "", "", fmt.Errorf("Caiyun translation request failed: %w", err)
	}
	var result struct {
		Code      int      `json:"rc"`
		Direction string   `json:"trans_type"`
		Target    []string `json:"target"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Code != 0 || len(result.Target) != 1 || strings.TrimSpace(result.Target[0]) == "" {
		return "", "", errors.New("Caiyun translation returned an invalid response")
	}
	return result.Target[0], languageFromDirection(result.Direction), nil
}

// TokenHub offers hy-mt2-lite via an OpenAI-compatible Chat Completions API:
// https://cloud.tencent.com/document/product/1823/130079.
func callTencentTokenHubTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	if err := trialTarget(target); err != nil {
		return "", "", err
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return "", "", errors.New("TokenHub API key is required")
	}
	host, err := trialHost(cfg.BaseURL, "tokenhub.tencentmaas.com", "tokenhub.tencentmaas.com", "tokenhub-intl.tencentmaas.com")
	if err != nil {
		return "", "", err
	}
	language := "Simplified Chinese"
	if target == "en" {
		language = "English"
	}
	encoded, _ := json.Marshal(map[string]any{
		"model":  "hy-mt2-lite",
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": "Translate the user's text into " + language + ". Return only the translation. Preserve formatting, numbers, formulas, and proper names."},
			{"role": "user", "content": text},
		},
	})
	body, err := trialJSONRequest(ctx, client, "https://"+host+"/v1/chat/completions", encoded, http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer " + key},
	})
	if err != nil {
		return "", "", fmt.Errorf("TokenHub translation request failed: %w", err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", "", errors.New("TokenHub translation returned an invalid response")
	}
	if result.Choices[0].FinishReason == "length" {
		return "", "", errors.New("TokenHub translation was truncated")
	}
	// TokenHub does not report a detected source language.
	return strings.TrimSpace(result.Choices[0].Message.Content), "", nil
}
