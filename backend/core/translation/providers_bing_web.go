package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
)

const bingTranslatorPageURL = "https://www.bing.com/translator"

var (
	bingCredentialsPattern = regexp.MustCompile(`params_AbusePreventionHelper\s*=\s*\[\s*(\d+)\s*,\s*["']([^"']+)["']`)
	bingIGPattern          = regexp.MustCompile(`(?i)["']ig["']\s*:\s*["']([a-f0-9]{16,})["']`)
	bingIIDPattern         = regexp.MustCompile(`(?i)data-iid\s*=\s*["']([a-z0-9._-]+)["']`)
)

// Bing's public translator page exposes a short-lived, anonymous session.
// This website endpoint is undocumented and may change or rate-limit requests.
func callBingTranslateWeb(ctx context.Context, client *http.Client, text, target string) (translated, source string, err error) {
	to := target
	if target == "zh" {
		to = "zh-Hans"
	} else if target != "en" {
		return "", "", errors.New("unsupported Bing web translation target")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", "", err
	}
	session := *webTranslationClient(client)
	session.Jar = jar
	session.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 || request.URL.Scheme != "https" || !bingTranslatorHost(request.URL.Hostname()) || request.URL.Port() != "" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	pageRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, bingTranslatorPageURL, nil)
	if err != nil {
		return "", "", err
	}
	pageRequest.Header.Set("User-Agent", bingWebUserAgent)
	pageResponse, err := session.Do(pageRequest)
	if err != nil {
		return "", "", fmt.Errorf("Bing translator session request failed: %w", err)
	}
	var pageURL *url.URL
	if pageResponse.Request != nil {
		pageURL = pageResponse.Request.URL
	}
	page, err := webTranslationBody(pageResponse)
	if err != nil {
		return "", "", fmt.Errorf("Bing translator session: %w", err)
	}
	if pageURL == nil || pageURL.Scheme != "https" || !bingTranslatorHost(pageURL.Hostname()) || pageURL.Port() != "" {
		return "", "", errors.New("Bing translator redirected to an unexpected origin")
	}
	credentials := bingCredentialsPattern.FindSubmatch(page)
	ig := bingIGPattern.FindSubmatch(page)
	iids := bingIIDPattern.FindAllSubmatch(page, -1)
	if len(credentials) != 3 || len(ig) != 2 || len(iids) == 0 {
		return "", "", errors.New("Bing translator session is unavailable")
	}
	iid := iids[len(iids)-1][1]
	query := url.Values{"isVertical": {"1"}, "IG": {string(ig[1])}, "IID": {string(iid)}}
	endpoint := &url.URL{Scheme: pageURL.Scheme, Host: pageURL.Host, Path: "/ttranslatev3", RawQuery: query.Encode()}
	form := url.Values{"fromLang": {"auto-detect"}, "to": {to}, "text": {text}, "token": {string(credentials[2])}, "key": {string(credentials[1])}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	request.Header.Set("User-Agent", bingWebUserAgent)
	request.Header.Set("Referer", pageURL.String())
	// A redirect must not replay the anonymous token and source text to another host.
	session.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := session.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("Bing web translation request failed: %w", err)
	}
	body, err := webTranslationBody(response)
	if err != nil {
		return "", "", fmt.Errorf("Bing web translation: %w", err)
	}
	var serviceError struct {
		StatusCode int `json:"statusCode"`
	}
	if json.Unmarshal(body, &serviceError) == nil && serviceError.StatusCode != 0 {
		return "", "", fmt.Errorf("Bing web translation service returned status %d", serviceError.StatusCode)
	}
	var result []struct {
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
		DetectedLanguage struct {
			Language string `json:"language"`
		} `json:"detectedLanguage"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result) == 0 || len(result[0].Translations) == 0 || strings.TrimSpace(result[0].Translations[0].Text) == "" {
		return "", "", errors.New("Bing web translation returned an invalid response")
	}
	source = result[0].DetectedLanguage.Language
	if strings.HasPrefix(strings.ToLower(source), "zh") {
		source = "zh"
	}
	return html.UnescapeString(result[0].Translations[0].Text), source, nil
}

const bingWebUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"

func bingTranslatorHost(host string) bool {
	host = strings.ToLower(host)
	return host == "bing.com" || strings.HasSuffix(host, ".bing.com")
}
