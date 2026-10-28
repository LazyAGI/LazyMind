package modelprovider

import (
	"lazymind/core/common/orm"
	"strings"
)

// providerProtocolDeclaration records official API formats, not runtime routes.
// Reviewed 2026-10-26. Keep in sync with frontend providerProtocols.ts; the parity
// test makes divergence visible. Sources describe model/parameter limitations.
type providerProtocolDeclaration struct {
	Protocols []string
	Sources   []string
}

// Every built-in catalog service is explicit. Never inherit a brand's protocols
// into a plan or infer support from the saved adapter, host or model names.
var providerProtocolDeclarations = map[string]providerProtocolDeclaration{
	"Doubao":             {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://docs.volcengine.com/docs/ark/integrate-third-party-tools?lang=zh", "https://docs.volcengine.com/docs/ark/quick-start?lang=en"}},
	"DoubaoCodingPlan":   {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=zh"}},
	"DoubaoAgentPlan":    {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://docs.volcengine.com/docs/ark/agent-plan-personal-other-tools?lang=zh", "https://docs.volcengine.com/docs/ark/integrate-third-party-tools?lang=zh"}},
	"SenseNova":          {Protocols: []string{"openai", "native"}, Sources: []string{"https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/overview/compatible-mode/", "https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/chat/GetStarted/APIList/"}},
	"SenseNovaTokenPlan": {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://platform.sensenova.cn/docs", "https://platform.sensenova.cn/docs#api-compatibility", "https://platform.sensenova.cn/docs#api-responses"}},
	"Qwen":               {Protocols: []string{"openai", "anthropic", "native"}, Sources: []string{"https://help.aliyun.com/zh/model-studio/base-url"}},
	"QwenCodingPlan":     {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://help.aliyun.com/zh/model-studio/coding-plan-faq"}},
	"QwenTokenPlan":      {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://help.aliyun.com/zh/model-studio/token-plan-personal-quick-start"}},
	"QwenTokenPlanTeam":  {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://help.aliyun.com/zh/model-studio/token-plan-team-quickstart"}},
	"GLM":                {Protocols: []string{"openai", "native"}, Sources: []string{"https://docs.bigmodel.cn/cn/guide/develop/openai/introduction", "https://docs.bigmodel.cn/cn/guide/start/quick-start"}},
	"GLMCodingPlan":      {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://docs.bigmodel.cn/cn/coding-plan/tool/others"}},
	"Kimi":               {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://platform.kimi.com/docs/get-api-key"}},
	"KimiCode":           {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://www.kimi.com/code/docs/en/kimi-code/models"}},
	"Minimax":            {Protocols: []string{"openai", "openai-responses", "anthropic", "native"}, Sources: []string{"https://platform.minimax.io/docs/api-reference/text-openai-api", "https://platform.minimax.io/docs/api-reference/responses-create", "https://platform.minimax.io/docs/api-reference/text-anthropic-api", "https://platform.minimax.io/docs/api-reference/text-post"}},
	"MiniMaxMPlan":       {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://platform.minimax.io/docs/m-plan/other-tools"}},
	"Claude":             {Protocols: []string{"anthropic", "openai"}, Sources: []string{"https://platform.claude.com/docs/en/api/overview", "https://platform.claude.com/docs/en/api/openai-sdk"}},
	"DeepSeek":           {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://api-docs.deepseek.com/", "https://api-docs.deepseek.com/guides/responses_api/", "https://api-docs.deepseek.com/guides/anthropic_api/"}},
	"OpenAI":             {Protocols: []string{"openai", "openai-responses"}, Sources: []string{"https://developers.openai.com/api/reference/overview"}},
	"OpenRouter":         {Protocols: []string{"openai", "openai-responses", "anthropic"}, Sources: []string{"https://openrouter.ai/docs/api/reference/overview", "https://openrouter.ai/tool/web-search/"}},
	"SiliconFlow":        {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://docs.siliconflow.cn/docs/userguide/introduction"}},
	"vLLM":               {Protocols: []string{"openai", "openai-responses", "anthropic", "native"}, Sources: []string{"https://docs.vllm.ai/en/stable/serving/online_serving/"}},
	"LMDeploy":           {Protocols: []string{"openai", "anthropic"}, Sources: []string{"https://lmdeploy.readthedocs.io/en/latest/llm/api_server.html", "https://lmdeploy.readthedocs.io/en/latest/llm/api_server_anthropic.html"}},
}

// providerDeclaresProtocol is a pure official declaration lookup. False means
// undeclared, not proof that the vendor rejects that protocol. Custom suppliers
// intentionally have no official declaration even when their names collide.
func providerDeclaresProtocol(provider orm.UserModelProvider, protocol string) bool {
	if provider.IsCustom {
		return false
	}
	declaration, ok := providerProtocolDeclarations[strings.TrimSpace(provider.Name)]
	if !ok {
		return false
	}
	for _, supported := range declaration.Protocols {
		if protocol == supported {
			return true
		}
	}
	return false
}

// runtimeSupportsRequestProtocol describes only the current adapter surface.
// Token Plan's documented Messages API requires Bearer authentication, whereas
// the current Claude adapter sends x-api-key. Do not enable that route yet.
func runtimeSupportsRequestProtocol(provider orm.UserModelProvider, protocol string) bool {
	return protocol == "openai" || protocol == "anthropic" && (provider.IsCustom || provider.Name != "SenseNovaTokenPlan")
}
