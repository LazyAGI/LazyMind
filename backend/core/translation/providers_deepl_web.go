package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// DeepL's consumer translation endpoint is separate from its authenticated API.
// It is undocumented for third-party use and may change or rate-limit requests.
const deepLWebURL = "https://oneshot-free.www.deepl.com/v1/translate"

func callDeepLWeb(ctx context.Context, client *http.Client, text, target string) (translated, source string, err error) {
	var targetLanguage string
	switch target {
	case "zh":
		targetLanguage = "zh-Hans"
	case "en":
		targetLanguage = "en"
	default:
		return "", "", errors.New("unsupported DeepL web translation target")
	}

	payload, err := json.Marshal(struct {
		Text       []string `json:"text"`
		TargetLang string   `json:"target_lang"`
	}{Text: []string{text}, TargetLang: targetLanguage})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepLWebURL, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "None")
	req.Header.Set("Content-Type", "application/json")

	// Keep requests on DeepL's fixed host even if the consumer endpoint moves.
	requestClient := *webTranslationClient(client)
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := requestClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("DeepL web translation request failed: %w", err)
	}
	body, err := webTranslationBody(resp)
	if err != nil {
		return "", "", fmt.Errorf("DeepL web translation: %w", err)
	}
	var result struct {
		Translations []struct {
			Text                   string `json:"text"`
			DetectedSourceLanguage string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Translations) != 1 || strings.TrimSpace(result.Translations[0].Text) == "" {
		return "", "", errors.New("DeepL web translation returned an invalid response")
	}
	return result.Translations[0].Text, result.Translations[0].DetectedSourceLanguage, nil
}
