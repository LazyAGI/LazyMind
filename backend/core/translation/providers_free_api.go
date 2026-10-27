package translation

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lazymind/core/modelconfig"
)

// Each adapter uses a fixed official endpoint. The saved BaseURL is checked only
// to reject stale or user-supplied hosts; it is never used to build a request.
func requireOfficialHost(raw, host string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), host) || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid translation endpoint for %s", host)
	}
	return nil
}

func requiredKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" || strings.Contains(key, "|") {
		return "", errors.New("invalid translation credentials")
	}
	return key, nil
}

func keyPair(raw string) (string, string, error) {
	id, secret, ok := strings.Cut(strings.TrimSpace(raw), "|")
	id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
	if !ok || id == "" || secret == "" || strings.Contains(secret, "|") {
		return "", "", errors.New("invalid translation credentials")
	}
	return id, secret, nil
}

func randomHex(bytesCount int) (string, error) {
	b := make([]byte, bytesCount)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func newFreeAPIRequest(ctx context.Context, method, endpoint, contentType string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func readFreeAPIResponse(client *http.Client, req *http.Request) ([]byte, int, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	// A redirect could send an API key or signed query to another host.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		// net/http wraps transport errors in url.Error, whose string contains
		// the complete URL (including Google Cloud's API key query parameter).
		if errors.Is(err, context.Canceled) {
			return nil, 0, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, context.DeadlineExceeded
		}
		return nil, 0, errors.New("translation request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func translationHTTPError(provider string, status int) error {
	return fmt.Errorf("%s translation returned HTTP %d", provider, status)
}

func callBaiduTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	const host = "fanyi-api.baidu.com"
	if err := requireOfficialHost(cfg.BaseURL, host); err != nil {
		return "", "", err
	}
	id, secret, err := keyPair(cfg.APIKey)
	if err != nil {
		return "", "", err
	}
	salt, err := randomHex(8)
	if err != nil {
		return "", "", err
	}
	// Baidu standard free tier accepts at most 1,000 characters per request.
	if len([]rune(text)) > 1000 {
		return "", "", errors.New("Baidu translation exceeds 1000 characters per request")
	}
	sum := md5.Sum([]byte(id + text + salt + secret))
	form := url.Values{"q": {text}, "from": {"auto"}, "to": {target}, "appid": {id}, "salt": {salt}, "sign": {hex.EncodeToString(sum[:])}}
	req, err := newFreeAPIRequest(ctx, http.MethodPost, "https://"+host+"/api/trans/vip/translate", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	data, status, err := readFreeAPIResponse(client, req)
	if err != nil {
		return "", "", err
	}
	var out struct {
		From      string `json:"from"`
		ErrorCode string `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
		Results   []struct {
			Dst string `json:"dst"`
		} `json:"trans_result"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", translationHTTPError("Baidu", status)
	}
	if out.ErrorCode != "" {
		return "", "", fmt.Errorf("Baidu translation failed (%s): %s", out.ErrorCode, out.ErrorMsg)
	}
	if status != http.StatusOK {
		return "", "", translationHTTPError("Baidu", status)
	}
	parts := make([]string, 0, len(out.Results))
	for _, result := range out.Results {
		parts = append(parts, result.Dst)
	}
	if len(parts) == 0 {
		return "", "", errors.New("Baidu translation returned no text")
	}
	return strings.Join(parts, "\n"), out.From, nil
}

func callAliyunTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	const host = "mt.cn-hangzhou.aliyuncs.com"
	if err := requireOfficialHost(cfg.BaseURL, host); err != nil {
		return "", "", err
	}
	id, secret, err := keyPair(cfg.APIKey)
	if err != nil {
		return "", "", err
	}
	nonce, err := randomHex(16)
	if err != nil {
		return "", "", err
	}
	form := url.Values{"FormatType": {"text"}, "Scene": {"general"}, "SourceLanguage": {"auto"}, "SourceText": {text}, "TargetLanguage": {target}}
	payload := []byte(form.Encode())
	req, err := newFreeAPIRequest(ctx, http.MethodPost, "https://"+host+"/", "application/x-www-form-urlencoded", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	// RPC V3 signing and form parameter placement:
	// https://help.aliyun.com/en/sdk/product-overview/v3-request-structure-and-signature
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	hash := sha256Hex(payload)
	const signedHeaders = "content-type;host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version"
	canonicalHeaders := "content-type:application/x-www-form-urlencoded\nhost:" + host + "\nx-acs-action:TranslateGeneral\nx-acs-content-sha256:" + hash + "\nx-acs-date:" + date + "\nx-acs-signature-nonce:" + nonce + "\nx-acs-version:2018-10-12\n"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + hash
	stringToSign := "ACS3-HMAC-SHA256\n" + sha256Hex([]byte(canonicalRequest))
	signature := hex.EncodeToString(hmacSHA256([]byte(secret), stringToSign))
	req.Header.Set("X-Acs-Action", "TranslateGeneral")
	req.Header.Set("X-Acs-Content-Sha256", hash)
	req.Header.Set("X-Acs-Date", date)
	req.Header.Set("X-Acs-Signature-Nonce", nonce)
	req.Header.Set("X-Acs-Version", "2018-10-12")
	req.Header.Set("Authorization", "ACS3-HMAC-SHA256 Credential="+id+",SignedHeaders="+signedHeaders+",Signature="+signature)
	data, status, err := readFreeAPIResponse(client, req)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Code    json.RawMessage `json:"Code"`
		Message string          `json:"Message"`
		Data    struct {
			Translated       string `json:"Translated"`
			DetectedLanguage string `json:"DetectedLanguage"`
		} `json:"Data"`
		Wrapped *struct {
			Code    json.RawMessage `json:"Code"`
			Message string          `json:"Message"`
			Data    struct {
				Translated       string `json:"Translated"`
				DetectedLanguage string `json:"DetectedLanguage"`
			} `json:"Data"`
		} `json:"TranslateGeneralResponse"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", translationHTTPError("Alibaba Cloud", status)
	}
	if out.Wrapped != nil {
		out.Code, out.Message, out.Data = out.Wrapped.Code, out.Wrapped.Message, out.Wrapped.Data
	}
	code := strings.Trim(string(out.Code), `"`)
	if code != "200" {
		return "", "", fmt.Errorf("Alibaba Cloud translation failed (%s): %s", code, out.Message)
	}
	if status != http.StatusOK {
		return "", "", translationHTTPError("Alibaba Cloud", status)
	}
	if out.Data.Translated == "" {
		return "", "", errors.New("Alibaba Cloud translation returned no text")
	}
	return out.Data.Translated, out.Data.DetectedLanguage, nil
}

func callVolcengineTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	const (
		host    = "translate.volcengineapi.com"
		region  = "cn-north-1"
		service = "translate"
	)
	if err := requireOfficialHost(cfg.BaseURL, host); err != nil {
		return "", "", err
	}
	id, secret, err := keyPair(cfg.APIKey)
	if err != nil {
		return "", "", err
	}
	payload, err := json.Marshal(map[string]any{"TargetLanguage": target, "TextList": []string{text}})
	if err != nil {
		return "", "", err
	}
	const query = "Action=TranslateText&Version=2020-06-01"
	req, err := newFreeAPIRequest(ctx, http.MethodPost, "https://"+host+"/?"+query, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	xDate := time.Now().UTC().Format("20060102T150405Z")
	shortDate := xDate[:8]
	contentHash := sha256Hex(payload)
	const signedHeaders = "content-type;host;x-content-sha256;x-date"
	canonicalHeaders := "content-type:application/json\nhost:" + host + "\nx-content-sha256:" + contentHash + "\nx-date:" + xDate + "\n"
	canonicalRequest := "POST\n/\n" + query + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + contentHash
	scope := shortDate + "/" + region + "/" + service + "/request"
	toSign := "HMAC-SHA256\n" + xDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte(secret), shortDate)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, toSign))
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", contentHash)
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+id+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	data, status, err := readFreeAPIResponse(client, req)
	if err != nil {
		return "", "", err
	}
	var out struct {
		TranslationList []struct {
			Translation            string `json:"Translation"`
			DetectedSourceLanguage string `json:"DetectedSourceLanguage"`
		} `json:"TranslationList"`
		ResponseMetadata struct {
			Error *struct {
				Code    json.RawMessage `json:"Code"`
				Message string          `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", translationHTTPError("Volcengine", status)
	}
	if out.ResponseMetadata.Error != nil {
		return "", "", fmt.Errorf("Volcengine translation failed (%s): %s", strings.Trim(string(out.ResponseMetadata.Error.Code), `"`), out.ResponseMetadata.Error.Message)
	}
	if status != http.StatusOK {
		return "", "", translationHTTPError("Volcengine", status)
	}
	if len(out.TranslationList) == 0 || out.TranslationList[0].Translation == "" {
		return "", "", errors.New("Volcengine translation returned no text")
	}
	return out.TranslationList[0].Translation, out.TranslationList[0].DetectedSourceLanguage, nil
}

func callMicrosoftTranslator(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	const host = "api.cognitive.microsofttranslator.com"
	if err := requireOfficialHost(cfg.BaseURL, host); err != nil {
		return "", "", err
	}
	key, region, _ := strings.Cut(strings.TrimSpace(cfg.APIKey), "|")
	key, region = strings.TrimSpace(key), strings.TrimSpace(region)
	if key == "" || strings.Contains(region, "|") {
		return "", "", errors.New("invalid translation credentials")
	}
	if target == "zh" {
		target = "zh-Hans"
	}
	params := url.Values{"api-version": {"3.0"}, "to": {target}}
	payload, _ := json.Marshal([]map[string]string{{"Text": text}})
	req, err := newFreeAPIRequest(ctx, http.MethodPost, "https://"+host+"/translate?"+params.Encode(), "application/json; charset=utf-8", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", key)
	if region != "" {
		req.Header.Set("Ocp-Apim-Subscription-Region", region)
	}
	data, status, err := readFreeAPIResponse(client, req)
	if err != nil {
		return "", "", err
	}
	var out []struct {
		DetectedLanguage struct {
			Language string `json:"language"`
		} `json:"detectedLanguage"`
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
	}
	if status != http.StatusOK {
		var failure struct {
			Error struct {
				Code    json.RawMessage `json:"code"`
				Message string          `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			return "", "", fmt.Errorf("Microsoft translation failed (%s): %s", strings.Trim(string(failure.Error.Code), `"`), failure.Error.Message)
		}
		return "", "", translationHTTPError("Microsoft", status)
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out) == 0 || len(out[0].Translations) == 0 || out[0].Translations[0].Text == "" {
		return "", "", errors.New("Microsoft translation returned no text")
	}
	return out[0].Translations[0].Text, out[0].DetectedLanguage.Language, nil
}

func callGoogleCloudTranslation(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	const host = "translation.googleapis.com"
	if err := requireOfficialHost(cfg.BaseURL, host); err != nil {
		return "", "", err
	}
	key, err := requiredKey(cfg.APIKey)
	if err != nil {
		return "", "", err
	}
	params := url.Values{"key": {key}}
	payload, _ := json.Marshal(map[string]string{"q": text, "target": target, "format": "text"})
	req, err := newFreeAPIRequest(ctx, http.MethodPost, "https://"+host+"/language/translate/v2?"+params.Encode(), "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	data, status, err := readFreeAPIResponse(client, req)
	if err != nil {
		return "", "", err
	}
	if status != http.StatusOK {
		var failure struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			return "", "", fmt.Errorf("Google Cloud translation failed (%d): %s", failure.Error.Code, failure.Error.Message)
		}
		return "", "", translationHTTPError("Google Cloud", status)
	}
	var out struct {
		Data struct {
			Translations []struct {
				TranslatedText         string `json:"translatedText"`
				DetectedSourceLanguage string `json:"detectedSourceLanguage"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data.Translations) == 0 || out.Data.Translations[0].TranslatedText == "" {
		return "", "", errors.New("Google Cloud translation returned no text")
	}
	return out.Data.Translations[0].TranslatedText, out.Data.Translations[0].DetectedSourceLanguage, nil
}

func callNiutrans(ctx context.Context, client *http.Client, cfg modelconfig.TranslationConfig, text, target string) (translated, source string, err error) {
	const host = "api.niutrans.com"
	if err := requireOfficialHost(cfg.BaseURL, host); err != nil {
		return "", "", err
	}
	key, err := requiredKey(cfg.APIKey)
	if err != nil {
		return "", "", err
	}
	form := url.Values{"apikey": {key}, "from": {"auto"}, "to": {target}, "src_text": {text}}
	req, err := newFreeAPIRequest(ctx, http.MethodPost, "https://"+host+"/NiuTransServer/translation", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	data, status, err := readFreeAPIResponse(client, req)
	if err != nil {
		return "", "", err
	}
	var out struct {
		TgtText   string          `json:"tgt_text"`
		From      string          `json:"from"`
		ErrorCode json.RawMessage `json:"error_code"`
		ErrorMsg  string          `json:"error_msg"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", translationHTTPError("Niutrans", status)
	}
	if len(out.ErrorCode) > 0 && string(out.ErrorCode) != `"0"` && string(out.ErrorCode) != "0" {
		return "", "", fmt.Errorf("Niutrans translation failed (%s): %s", strings.Trim(string(out.ErrorCode), `"`), out.ErrorMsg)
	}
	if status != http.StatusOK {
		return "", "", translationHTTPError("Niutrans", status)
	}
	if out.TgtText == "" {
		return "", "", errors.New("Niutrans translation returned no text")
	}
	return out.TgtText, out.From, nil
}
