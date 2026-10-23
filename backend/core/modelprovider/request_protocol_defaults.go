package modelprovider

import (
	"net/url"
	"strings"
)

// These mappings apply only to documented API roots on the official host.
// Proxy hosts and tenant prefixes retain the user's own routing contract.
func knownSupplierProtocolDefaults(base string) (map[string]string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "ark.cn-beijing.volces.com") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, false
	}
	var openai, anthropic string
	switch strings.TrimRight(parsed.EscapedPath(), "/") {
	// https://console.volcengine.com/ark/region:cn-beijing/docs/go-live-faq
	case "/api/v3", "/api/compatible":
		openai, anthropic = "/api/v3/", "/api/compatible"
	// https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=en
	case "/api/coding/v3", "/api/coding":
		openai, anthropic = "/api/coding/v3/", "/api/coding"
	default:
		return nil, false
	}
	root := "https://" + parsed.Host
	return map[string]string{"openai": root + openai, "anthropic": root + anthropic}, true
}

func repairInheritedProtocolDefaults(config *savedProviderRequestConfig, stored savedProviderRequestConfig) {
	// Version-one metadata retains the original OpenAI root even after users
	// switch protocols or set a different active endpoint. Repair only the old
	// derived Anthropic default, never an independently configured address.
	anchor := stored.Defaults["openai"]
	if known, ok := knownSupplierProtocolDefaults(anchor); ok {
		replaceInheritedProtocolDefault(config, stored, "anthropic", strings.TrimRight(strings.TrimSpace(anchor), "/"), known["anthropic"])
	}
	// Native Anthropic suppliers previously derived OpenAI by appending /v1/.
	// The saved Anthropic root is the anchor for those snapshots, not the
	// currently active address, which may be an explicit proxy override.
	anthropic := stored.Defaults["anthropic"]
	if known, ok := knownSupplierProtocolDefaults(anthropic); ok && normalizeBaseURLForCompare(anthropic) == normalizeBaseURLForCompare(known["anthropic"]) {
		oldOpenAI := strings.TrimRight(strings.TrimSpace(anthropic), "/") + "/v1/"
		replaceInheritedProtocolDefault(config, stored, "openai", oldOpenAI, known["openai"])
		replaceInheritedProtocolDefault(config, stored, "openai-responses", oldOpenAI, known["openai"])
	}
}

func replaceInheritedProtocolDefault(config *savedProviderRequestConfig, stored savedProviderRequestConfig, protocol, oldDefault, replacement string) {
	if normalizeBaseURLForCompare(stored.Defaults[protocol]) == normalizeBaseURLForCompare(oldDefault) {
		config.Defaults[protocol] = replacement
		if endpoint := config.Endpoints[protocol]; endpoint != "" && normalizeBaseURLForCompare(endpoint) == normalizeBaseURLForCompare(oldDefault) {
			config.Endpoints[protocol] = ""
		}
	}
}
