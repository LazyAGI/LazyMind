package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// These services use the same public, keyless endpoints as browser translation
// extensions. Google GTX and Tencent TranSmart are undocumented web endpoints;
// their response formats and availability can change without notice.
const (
	googleTranslateWebURL  = "https://translate.googleapis.com/translate_a/t"
	tencentTranSmartWebURL = "https://transmart.qq.com/api/imt"
	myMemoryURL            = "https://api.mymemory.translated.net/get"
	webResponseLimit       = 1 << 20
)

func webTranslationClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func webTranslationBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("translation service returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > webResponseLimit {
		return nil, errors.New("translation response is too large")
	}
	return body, nil
}

func callGoogleTranslateWeb(ctx context.Context, client *http.Client, text, target string) (translated, source string, err error) {
	if target != "zh" && target != "en" {
		return "", "", errors.New("unsupported Google web translation target")
	}
	endpoint, _ := url.Parse(googleTranslateWebURL)
	query := endpoint.Query()
	query.Set("client", "gtx")
	query.Set("dt", "t")
	query.Set("sl", "auto")
	query.Set("tl", target)
	endpoint.RawQuery = query.Encode()
	form := url.Values{"q": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := webTranslationClient(client).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("Google web translation request failed: %w", err)
	}
	body, err := webTranslationBody(resp)
	if err != nil {
		return "", "", fmt.Errorf("Google web translation: %w", err)
	}
	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) == 0 || len(rows[0]) == 0 || strings.TrimSpace(rows[0][0]) == "" {
		return "", "", errors.New("Google web translation returned an invalid response")
	}
	translated = html.UnescapeString(rows[0][0])
	if len(rows[0]) > 1 {
		source = rows[0][1]
	}
	return translated, source, nil
}

func callTencentTranSmartWeb(ctx context.Context, client *http.Client, text, target string) (translated, source string, err error) {
	if target != "zh" && target != "en" {
		return "", "", errors.New("unsupported TranSmart translation target")
	}
	payload := map[string]any{
		"header": map[string]string{
			"fn":         "auto_translation",
			"client_key": "browser-chrome-150.0.0-Windows 10-lazymind-" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		},
		"type":           "plain",
		"model_category": "normal",
		"source": map[string]any{
			"lang": "auto", "text_list": []string{text},
		},
		"target": map[string]string{"lang": target},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tencentTranSmartWebURL, bytes.NewReader(encoded))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://transmart.qq.com/zh-CN/index")
	resp, err := webTranslationClient(client).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("TranSmart translation request failed: %w", err)
	}
	body, err := webTranslationBody(resp)
	if err != nil {
		return "", "", fmt.Errorf("TranSmart translation: %w", err)
	}
	var result struct {
		Header struct {
			RetCode string `json:"ret_code"`
		} `json:"header"`
		AutoTranslation []string `json:"auto_translation"`
		SourceLanguage  string   `json:"src_lang"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", errors.New("TranSmart translation returned an invalid response")
	}
	if result.Header.RetCode != "succ" {
		return "", "", fmt.Errorf("TranSmart translation failed: %s", result.Header.RetCode)
	}
	if len(result.AutoTranslation) != 1 || strings.TrimSpace(result.AutoTranslation[0]) == "" {
		return "", "", errors.New("TranSmart translation returned an invalid response")
	}
	return html.UnescapeString(result.AutoTranslation[0]), result.SourceLanguage, nil
}

func validateMyMemoryLanguagePair(source, target string) error {
	source, target = strings.ToLower(strings.TrimSpace(source)), strings.ToLower(strings.TrimSpace(target))
	if source != "en" && source != "zh" {
		return errors.New("MyMemory requires an explicit source language: en or zh")
	}
	if target != "en" && target != "zh" {
		return errors.New("unsupported MyMemory translation target")
	}
	if source == target {
		return errors.New("MyMemory source and target languages must differ")
	}
	return nil
}

func callMyMemory(ctx context.Context, client *http.Client, text, source, target string) (translated, detectedSource string, err error) {
	if len([]byte(text)) > 500 {
		return "", "", errors.New("MyMemory request exceeds 500 bytes")
	}
	if err := validateMyMemoryLanguagePair(source, target); err != nil {
		return "", "", err
	}
	source, target = strings.ToLower(strings.TrimSpace(source)), strings.ToLower(strings.TrimSpace(target))
	var sourceCode, targetCode string
	if source == "zh" {
		sourceCode = "zh-CN"
	} else {
		sourceCode = "en"
	}
	if target == "zh" {
		targetCode = "zh-CN"
	} else {
		targetCode = "en"
	}
	endpoint, _ := url.Parse(myMemoryURL)
	query := endpoint.Query()
	query.Set("q", text)
	query.Set("langpair", sourceCode+"|"+targetCode)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", "", err
	}
	resp, err := webTranslationClient(client).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("MyMemory translation request failed: %w", err)
	}
	body, err := webTranslationBody(resp)
	if err != nil {
		return "", "", fmt.Errorf("MyMemory translation: %w", err)
	}
	var result struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
		ResponseStatus  int    `json:"responseStatus"`
		ResponseDetails string `json:"responseDetails"`
		QuotaFinished   bool   `json:"quotaFinished"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", errors.New("MyMemory translation returned an invalid response")
	}
	if result.QuotaFinished {
		return "", "", errors.New("MyMemory free quota is exhausted")
	}
	if result.ResponseStatus != http.StatusOK || strings.TrimSpace(result.ResponseData.TranslatedText) == "" {
		return "", "", fmt.Errorf("MyMemory translation failed: %s", result.ResponseDetails)
	}
	return html.UnescapeString(result.ResponseData.TranslatedText), source, nil
}
