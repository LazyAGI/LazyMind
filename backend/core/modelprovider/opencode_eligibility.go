package modelprovider

import "strings"

const EvoModelKey = "evo_llm"

// OpenCodeModelDescriptor is the execution contract consumed by the Evo
// service. Core owns eligibility; the worker only validates this descriptor.
type OpenCodeModelDescriptor struct {
	Provider      string `json:"provider"`
	ProviderModel string `json:"provider_model"`
	Model         string `json:"model"`
	NPM           string `json:"npm"`
	BaseURL       string `json:"base_url"`
}

type openCodeProviderSpec struct {
	provider string
	npm      string
	models   map[string]struct{}
	rewrites map[string]string
}

func modelSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return out
}

var openCodeProviderAliases = map[string]string{
	"alibaba": "qwen", "alibabacn": "qwen", "anthropic": "claude",
	"claude": "claude", "dashscope": "qwen", "deepseek": "deepseek",
	"glm": "glm", "kimi": "kimi", "minimax": "minimax",
	"moonshot": "kimi", "moonshotai": "kimi", "openai": "openai",
	"qwen": "qwen", "sensenova": "sensenova", "sensetime": "sensenova",
	"siliconflow": "siliconflow", "zhipu": "glm", "zhipuai": "glm",
}

var openCodeProviders = map[string]openCodeProviderSpec{
	"claude": {
		provider: "anthropic", npm: "@ai-sdk/anthropic",
		models: modelSet("claude-haiku-4-5", "claude-opus-4-7", "claude-sonnet-4-6"),
	},
	"deepseek": {
		provider: "deepseek", npm: "@ai-sdk/openai-compatible",
		models:   modelSet("deepseek-v4-flash", "deepseek-v4-pro"),
		rewrites: map[string]string{"https://api.deepseek.com/v1": "https://api.deepseek.com"},
	},
	"glm": {
		provider: "zhipuai", npm: "@ai-sdk/openai-compatible",
		models: modelSet(
			"GLM-4.5-Air", "GLM-4.5-Flash", "GLM-4.6", "GLM-4.7",
			"GLM-4.7-Flash", "GLM-4.7-FlashX", "GLM-5", "GLM-5.1", "GLM-5V-Turbo",
		),
	},
	"kimi": {
		provider: "moonshotai-cn", npm: "@ai-sdk/openai-compatible",
		models: modelSet(
			"kimi-k2-0711-preview", "kimi-k2-0905-preview", "kimi-k2-thinking",
			"kimi-k2-thinking-turbo", "kimi-k2-turbo-preview", "kimi-k2.5", "kimi-k2.6",
		),
		rewrites: map[string]string{"https://api.moonshot.cn": "https://api.moonshot.cn/v1"},
	},
	"minimax": {
		provider: "minimax-cn", npm: "@ai-sdk/anthropic",
		models:   modelSet("MiniMax-M2.5", "MiniMax-M2.5-highspeed", "MiniMax-M2.7", "MiniMax-M2.7-highspeed"),
		rewrites: map[string]string{"https://api.minimaxi.com/v1": "https://api.minimaxi.com/anthropic/v1"},
	},
	"openai": {
		provider: "openai", npm: "@ai-sdk/openai",
		models: modelSet(
			"gpt-4.1", "gpt-4.1-mini", "gpt-4o-mini", "gpt-5", "gpt-5-mini", "gpt-5-nano",
			"gpt-5-pro", "gpt-5.1", "gpt-5.2", "gpt-5.2-pro", "gpt-5.4",
			"gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.4-pro", "gpt-5.5", "gpt-5.5-pro", "o3",
		),
		rewrites: map[string]string{"https://api.openai.com": "https://api.openai.com/v1"},
	},
	"qwen": {
		provider: "alibaba-cn", npm: "@ai-sdk/openai-compatible",
		models: modelSet(
			"qwen-plus", "qwen-max", "qwen3-max", "qwen3.5-flash", "qwen3.5-plus",
			"qwen3.6-flash", "qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus",
			"qwen3-coder-flash", "qwen3-coder-plus", "qwen3-coder-30b-a3b-instruct",
			"qwen3-coder-480b-a35b-instruct",
		),
		rewrites: map[string]string{"https://dashscope.aliyuncs.com": "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	},
	"sensenova": {
		provider: "sensenova", npm: "@ai-sdk/openai-compatible",
		models: modelSet("deepseek-v4-flash"),
	},
	"siliconflow": {
		provider: "siliconflow-cn", npm: "@ai-sdk/openai-compatible",
		models: modelSet(
			"deepseek-ai/DeepSeek-V4-Flash", "MiniMaxAI/MiniMax-M2.5",
			"Pro/MiniMaxAI/MiniMax-M2.5", "Pro/deepseek-ai/DeepSeek-V3.2",
			"Pro/moonshotai/Kimi-K2.5", "Pro/moonshotai/Kimi-K2.6",
			"Pro/zai-org/GLM-5", "Pro/zai-org/GLM-5.1",
			"Qwen/Qwen3-Coder-30B-A3B-Instruct", "Qwen/Qwen3-Coder-480B-A35B-Instruct",
		),
	},
}

// ResolveOpenCodeModel returns a descriptor only for production model rows
// that OpenCode can execute. Built-in rows stay restricted to the curated
// model allowlist, while user-added chat models may reuse a known provider's
// OpenCode adapter after their connection group has been verified.
func ResolveOpenCodeModel(providerName, modelName, baseURL, technicalType string, isDefault bool) (OpenCodeModelDescriptor, bool) {
	technicalType = strings.ToLower(strings.TrimSpace(technicalType))
	if technicalType != "llm" && technicalType != "vlm" {
		return OpenCodeModelDescriptor{}, false
	}
	providerKey := openCodeProviderAliases[normalizeProviderName(providerName)]
	spec, ok := openCodeProviders[providerKey]
	if !ok {
		return OpenCodeModelDescriptor{}, false
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return OpenCodeModelDescriptor{}, false
	}
	if isDefault {
		if _, ok := spec.models[strings.ToLower(modelName)]; !ok {
			return OpenCodeModelDescriptor{}, false
		}
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return OpenCodeModelDescriptor{}, false
	}
	if rewritten := spec.rewrites[baseURL]; rewritten != "" {
		baseURL = rewritten
	}
	return OpenCodeModelDescriptor{
		Provider: spec.provider, ProviderModel: modelName,
		Model: spec.provider + "/" + modelName, NPM: spec.npm, BaseURL: baseURL,
	}, true
}
