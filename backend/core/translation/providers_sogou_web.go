package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
)

const (
	sogouTranslateWebURL  = "https://fanyi.sogou.com/text"
	sogouStatePrefix      = "window.__INITIAL_STATE__="
	sogouBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"
)

type sogouTranslationState struct {
	TextTranslate struct {
		TranslateData struct {
			Translate struct {
				From      string          `json:"from"`
				To        string          `json:"to"`
				Dit       string          `json:"dit"`
				ErrorCode json.RawMessage `json:"errorCode"`
			} `json:"translate"`
		} `json:"translateData"`
	} `json:"textTranslate"`
}

func callSogouTranslateWeb(ctx context.Context, client *http.Client, text, target string) (translated, source string, err error) {
	var targetCode string
	switch target {
	case "zh":
		targetCode = "zh-CHS"
	case "en":
		targetCode = "en"
	default:
		return "", "", errors.New("unsupported Sogou web translation target")
	}
	endpoint, _ := url.Parse(sogouTranslateWebURL)
	query := endpoint.Query()
	query.Set("keyword", text)
	query.Set("transfrom", "auto")
	query.Set("transto", targetCode)
	query.Set("model", "general")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", sogouBrowserUserAgent)
	requestClient := *webTranslationClient(client)
	requestClient.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 {
			return errors.New("Sogou web translation followed too many redirects")
		}
		if !sogouAllowedURL(next.URL) {
			return errors.New("Sogou web translation redirected to an unexpected origin")
		}
		return nil
	}
	resp, err := requestClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("Sogou web translation request failed: %w", err)
	}
	if resp.Request != nil && !sogouAllowedURL(resp.Request.URL) {
		resp.Body.Close()
		return "", "", errors.New("Sogou web translation returned a response from an unexpected origin")
	}
	body, err := webTranslationBody(resp)
	if err != nil {
		return "", "", fmt.Errorf("Sogou web translation: %w", err)
	}
	root, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return "", "", errors.New("Sogou web translation returned an invalid response")
	}
	stateJSON := findSogouStateJSON(root)
	var state sogouTranslationState
	if stateJSON == "" || json.NewDecoder(strings.NewReader(stateJSON)).Decode(&state) != nil {
		return "", "", errors.New("Sogou web translation returned an invalid response")
	}
	result := state.TextTranslate.TranslateData.Translate
	if strings.TrimSpace(result.From) == "" || strings.TrimSpace(result.Dit) == "" || string(result.ErrorCode) != `"0"` {
		return "", "", errors.New("Sogou web translation returned an invalid response")
	}
	if result.To != targetCode {
		return "", "", fmt.Errorf("Sogou web translation changed target from %s to %s", targetCode, result.To)
	}
	source = strings.ToLower(strings.TrimSpace(result.From))
	if source == "zh-chs" || source == "zh-cn" {
		source = "zh"
	}
	return strings.TrimSpace(result.Dit), source, nil
}

func sogouAllowedURL(endpoint *url.URL) bool {
	return endpoint != nil && endpoint.Scheme == "https" &&
		strings.EqualFold(endpoint.Hostname(), "fanyi.sogou.com") &&
		(endpoint.Port() == "" || endpoint.Port() == "443") && endpoint.User == nil
}

func findSogouStateJSON(node *xhtml.Node) string {
	if node.Type == xhtml.ElementNode && node.Data == "script" {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.TextNode {
				if index := strings.Index(child.Data, sogouStatePrefix); index >= 0 {
					return child.Data[index+len(sogouStatePrefix):]
				}
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if stateJSON := findSogouStateJSON(child); stateJSON != "" {
			return stateJSON
		}
	}
	return ""
}
