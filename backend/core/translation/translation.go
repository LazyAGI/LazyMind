package translation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const (
	tencentService = "tmt"
	tencentVersion = "2018-03-21"
	tencentAction  = "TextTranslate"
	tencentRegion  = "ap-beijing"
)

type request struct {
	Text   string `json:"text"`
	Target string `json:"target,omitempty"`
}

type response struct {
	TranslatedText string `json:"translated_text"`
	Source         string `json:"source"`
	Target         string `json:"target"`
}

type statusResponse struct {
	Configured bool `json:"configured"`
}

type tencentCredentials struct {
	SecretID  string
	SecretKey string
}

type tencentResponse struct {
	Response struct {
		Source string `json:"Source"`
		Target string `json:"Target"`
		Text   string `json:"TargetText"`
		Error  *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"Response"`
}

func Status(w http.ResponseWriter, r *http.Request) {
	config, err := modelconfig.LoadTranslationConfig(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load translation config failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, statusResponse{Configured: config != nil})
}

func Translate(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		common.ReplyErr(w, "text is required", http.StatusBadRequest)
		return
	}
	if len([]rune(req.Text)) > 5000 {
		common.ReplyErr(w, "text exceeds 5000 characters", http.StatusBadRequest)
		return
	}

	config, err := modelconfig.LoadTranslationConfig(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load translation config failed", http.StatusInternalServerError)
		return
	}
	if config == nil {
		common.ReplyErr(w, "translation service is not configured", http.StatusPreconditionFailed)
		return
	}
	if !strings.EqualFold(config.ProviderName, "Tencent Translation") {
		common.ReplyErr(w, "unsupported translation provider", http.StatusBadRequest)
		return
	}
	credentials, err := parseTencentCredentials(config.APIKey)
	if err != nil {
		common.ReplyErr(w, "invalid translation credentials", http.StatusPreconditionFailed)
		return
	}
	target := normalizeTarget(req.Target, req.Text)
	translated, source, err := callTencent(r.Context(), config.BaseURL, credentials, req.Text, target)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, response{TranslatedText: translated, Source: source, Target: target})
}

func normalizeTarget(target, text string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "zh" || target == "en" {
		return target
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return "en"
		}
	}
	return "zh"
}

func parseTencentCredentials(raw string) (tencentCredentials, error) {
	secretID, secretKey, ok := strings.Cut(strings.TrimSpace(raw), "|")
	secretID, secretKey = strings.TrimSpace(secretID), strings.TrimSpace(secretKey)
	if !ok || secretID == "" || secretKey == "" {
		return tencentCredentials{}, errors.New("SecretId and SecretKey are required")
	}
	return tencentCredentials{SecretID: secretID, SecretKey: secretKey}, nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func callTencent(ctx context.Context, baseURL string, credentials tencentCredentials, text, target string) (string, string, error) {
	payload, err := json.Marshal(map[string]any{
		"SourceText": text, "Source": "auto", "Target": target, "ProjectId": 0,
	})
	if err != nil {
		return "", "", err
	}
	endpoint, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || endpoint.Scheme != "https" || !strings.EqualFold(endpoint.Hostname(), "tmt.tencentcloudapi.com") {
		return "", "", errors.New("invalid Tencent translation endpoint")
	}
	timestamp := time.Now().Unix()
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	canonicalHeaders := "content-type:application/json; charset=utf-8\nhost:" + endpoint.Host + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + sha256Hex(payload)
	credentialScope := date + "/" + tencentService + "/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + fmt.Sprint(timestamp) + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	secretDate := hmacSHA256([]byte("TC3"+credentials.SecretKey), date)
	secretService := hmacSHA256(secretDate, tencentService)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))
	authorization := "TC3-HMAC-SHA256 Credential=" + credentials.SecretID + "/" + credentialScope + ", SignedHeaders=" + signedHeaders + ", Signature=" + signature

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Host", endpoint.Host)
	req.Header.Set("X-TC-Action", tencentAction)
	req.Header.Set("X-TC-Timestamp", fmt.Sprint(timestamp))
	req.Header.Set("X-TC-Version", tencentVersion)
	req.Header.Set("X-TC-Region", tencentRegion)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("Tencent translation request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	var decoded tencentResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", "", fmt.Errorf("Tencent translation returned HTTP %d", resp.StatusCode)
	}
	if decoded.Response.Error != nil {
		return "", "", fmt.Errorf("Tencent translation failed: %s", decoded.Response.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decoded.Response.Text == "" {
		return "", "", fmt.Errorf("Tencent translation returned HTTP %d", resp.StatusCode)
	}
	return decoded.Response.Text, decoded.Response.Source, nil
}
