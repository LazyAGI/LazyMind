package modelprovider

// LazyLLMSource converts the selected catalog provider into the source key
// registered by LazyLLM. The source depends only on the selected provider;
// using an official provider through a reverse proxy must not turn it into
// an OpenAI source.
func LazyLLMSource(providerName string) string {
	normalized := normalizeProviderName(providerName)
	aliases := map[string]string{
		"anthropic":     "claude",
		"bigmodel":      "glm",
		"moonshot":      "kimi",
		"tongyiqianwen": "qwen",
		"volcanoark":    "doubao",
		"volcengine":    "doubao",
		"zhipu":         "glm",
		"zhipuai":       "glm",
	}
	if source := aliases[normalized]; source != "" {
		return source
	}
	return normalized
}
