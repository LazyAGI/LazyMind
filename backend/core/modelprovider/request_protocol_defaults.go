package modelprovider

import (
	"net/url"
	"strings"

	"lazymind/core/common/orm"
)

// These mappings apply only to documented API roots on the official host.
// Proxy hosts and tenant prefixes retain the user's own routing contract.
func knownSupplierProtocolDefaults(base string) (map[string]string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, false
	}
	var openai, anthropic string
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	switch strings.ToLower(parsed.Host) {
	case "ark.cn-beijing.volces.com":
		switch path {
		// https://console.volcengine.com/ark/region:cn-beijing/docs/go-live-faq
		case "/api/v3", "/api/compatible":
			openai, anthropic = "/api/v3/", "/api/compatible"
		// https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=en
		case "/api/coding/v3", "/api/coding":
			openai, anthropic = "/api/coding/v3/", "/api/coding"
		case "/api/plan/v3", "/api/plan":
			openai, anthropic = "/api/plan/v3/", "/api/plan"
		}
	case "token.sensenova.cn":
		if path == "" || path == "/v1" {
			openai, anthropic = "/v1/", ""
		}
	case "coding.dashscope.aliyuncs.com":
		if path == "/v1" || path == "/apps/anthropic" {
			openai, anthropic = "/v1/", "/apps/anthropic"
		}
	case "token-plan.cn-beijing.maas.aliyuncs.com":
		if path == "/compatible-mode/v1" || path == "/apps/anthropic" {
			openai, anthropic = "/compatible-mode/v1/", "/apps/anthropic"
		}
	case "api.kimi.com":
		if path == "/coding" || path == "/coding/v1" {
			openai, anthropic = "/coding/v1/", "/coding/"
		}
	case "open.bigmodel.cn":
		// Both services share Anthropic's root. Its reverse mapping requires an
		// explicit service identity; the URL alone cannot select a billing plan.
		switch path {
		case "/api/coding/paas/v4":
			openai, anthropic = "/api/coding/paas/v4/", "/api/anthropic"
		case "/api/paas/v4":
			openai, anthropic = "/api/paas/v4/", "/api/anthropic"
		}
	case "api.minimax.cn":
		if path == "/v1" || path == "/anthropic" {
			openai, anthropic = "/v1/", "/anthropic"
		}
	default:
		return nil, false
	}
	if openai == "" {
		return nil, false
	}
	root := "https://" + parsed.Host
	return map[string]string{"openai": root + openai, "anthropic": root + anthropic}, true
}

func supplierProtocolDefaults(provider orm.UserModelProvider, base string) (map[string]string, bool) {
	if !provider.IsCustom {
		if profile, ok := providerServiceProfiles[provider.Name]; ok && profile.brand == "GLM" {
			u, err := url.Parse(strings.TrimSpace(base))
			if err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, "open.bigmodel.cn") && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.TrimRight(u.EscapedPath(), "/") == "/api/anthropic" {
				root := "https://" + u.Host
				path := "/api/paas/v4/"
				if profile.service == "coding" {
					path = "/api/coding/paas/v4/"
				}
				return map[string]string{"openai": root + path, "anthropic": root + "/api/anthropic"}, true
			}
		}
	}
	return knownSupplierProtocolDefaults(base)
}

func repairInheritedProtocolDefaults(config *savedProviderRequestConfig, stored savedProviderRequestConfig, provider orm.UserModelProvider) {
	// Version-one metadata retains the original OpenAI root even after users
	// switch protocols or set a different active endpoint. Repair only the old
	// derived Anthropic default, never an independently configured address.
	anchor := stored.Defaults["openai"]
	if known, ok := supplierProtocolDefaults(provider, anchor); ok {
		replaceInheritedProtocolDefault(config, stored, "anthropic", strings.TrimRight(strings.TrimSpace(anchor), "/"), known["anthropic"])
	}
	// Native Anthropic suppliers previously derived OpenAI by appending /v1/.
	// The saved Anthropic root is the anchor for those snapshots, not the
	// currently active address, which may be an explicit proxy override.
	anthropic := stored.Defaults["anthropic"]
	if known, ok := supplierProtocolDefaults(provider, anthropic); ok && normalizeBaseURLForCompare(anthropic) == normalizeBaseURLForCompare(known["anthropic"]) {
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
