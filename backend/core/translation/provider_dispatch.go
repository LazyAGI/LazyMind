package translation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"lazymind/core/modelconfig"
)

var providerRateGate = struct {
	sync.Mutex
	next map[string]time.Time
}{next: make(map[string]time.Time)}

func waitForProvider(ctx context.Context, userID string, spec providerSpec) error {
	if spec.MinInterval <= 0 {
		return nil
	}
	key := userID + "\x00" + spec.Name
	if spec.Name == "MyMemory" {
		// Anonymous requests share the backend egress IP and its public quota.
		key = spec.Name
	}
	providerRateGate.Lock()
	now := time.Now()
	at := now
	if next := providerRateGate.next[key]; next.After(now) {
		at = next
	}
	providerRateGate.next[key] = at.Add(spec.MinInterval)
	if len(providerRateGate.next) > 1000 {
		for name, next := range providerRateGate.next {
			if next.Before(now.Add(-time.Minute)) {
				delete(providerRateGate.next, name)
			}
		}
	}
	providerRateGate.Unlock()
	if delay := time.Until(at); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func splitProviderText(text string, spec providerSpec) ([]string, error) {
	if spec.MaxInputBytes > 0 {
		if len([]byte(text)) > spec.MaxInputBytes {
			return nil, fmt.Errorf("%s request exceeds %d bytes", spec.Name, spec.MaxInputBytes)
		}
		return []string{text}, nil
	}
	if spec.MaxChunkRunes <= 0 {
		return []string{text}, nil
	}
	runes := []rune(text)
	parts := make([]string, 0, (len(runes)+spec.MaxChunkRunes-1)/spec.MaxChunkRunes)
	for start := 0; start < len(runes); start += spec.MaxChunkRunes {
		end := min(start+spec.MaxChunkRunes, len(runes))
		parts = append(parts, string(runes[start:end]))
	}
	return parts, nil
}

func translateWithConfig(ctx context.Context, client *http.Client, userID string, config modelconfig.TranslationConfig, text, target string) (Response, error) {
	return translateWithConfigSource(ctx, client, userID, config, text, "", target)
}

func translateWithConfigSource(ctx context.Context, client *http.Client, userID string, config modelconfig.TranslationConfig, text, source, target string) (Response, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Response{}, errors.New("text is required")
	}
	if len([]rune(text)) > 5000 {
		return Response{}, errors.New("text exceeds 5000 characters")
	}
	spec, ok := lookupProvider(config.ProviderName)
	if !ok {
		return Response{}, errors.New("unsupported translation provider")
	}
	if !spec.Keyless && strings.TrimSpace(config.APIKey) == "" {
		return Response{}, errors.New("invalid translation credentials")
	}
	target = normalizeTarget(target, text)
	if spec.Name == "MyMemory" {
		if err := validateMyMemoryLanguagePair(source, target); err != nil {
			return Response{}, err
		}
	}
	parts, err := splitProviderText(text, spec)
	if err != nil {
		return Response{}, err
	}
	output := make([]string, 0, len(parts))
	detectedSource := ""
	for _, part := range parts {
		if err := waitForProvider(ctx, userID, spec); err != nil {
			return Response{}, err
		}
		translated, detected, err := callSelectedProvider(ctx, client, config, spec.Name, part, source, target)
		if err != nil {
			return Response{}, err
		}
		if detectedSource == "" {
			detectedSource = detected
		}
		output = append(output, translated)
	}
	return Response{TranslatedText: strings.Join(output, ""), Source: detectedSource, Target: target}, nil
}

func callSelectedProvider(ctx context.Context, client *http.Client, config modelconfig.TranslationConfig, name, text, source, target string) (string, string, error) {
	switch name {
	case "Tencent Translation":
		credentials, err := parseTencentCredentials(config.APIKey)
		if err != nil {
			return "", "", err
		}
		return callTencent(ctx, config.BaseURL, credentials, text, target)
	case "Google Translate Web":
		return callGoogleTranslateWeb(ctx, client, text, target)
	case "Tencent TranSmart Web":
		return callTencentTranSmartWeb(ctx, client, text, target)
	case "MyMemory":
		return callMyMemory(ctx, client, text, source, target)
	case "Baidu Translation":
		return callBaiduTranslation(ctx, client, config, text, target)
	case "Niutrans":
		return callNiutrans(ctx, client, config, text, target)
	case "Aliyun Translation":
		return callAliyunTranslation(ctx, client, config, text, target)
	case "Volcengine Translation":
		return callVolcengineTranslation(ctx, client, config, text, target)
	case "Microsoft Translator":
		return callMicrosoftTranslator(ctx, client, config, text, target)
	case "Google Cloud Translation":
		return callGoogleCloudTranslation(ctx, client, config, text, target)
	case "Youdao Translation":
		return callYoudaoTranslation(ctx, client, config, text, target)
	case "DeepL":
		return callDeepL(ctx, client, config, text, target)
	case "Caiyun Translation":
		return callCaiyunTranslation(ctx, client, config, text, target)
	case "Tencent TokenHub Translation":
		return callTencentTokenHubTranslation(ctx, client, config, text, target)
	default:
		return "", "", errors.New("unsupported translation provider")
	}
}
