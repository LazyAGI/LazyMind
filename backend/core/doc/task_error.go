package doc

import (
	"encoding/json"
	"strings"
)

// Parse-task failure codes (localized on frontend via errors.{code}).
const (
	parseTaskErrCodeSubmitFailed       = "2000720"
	parseTaskErrCodeRateLimit          = "2000721"
	parseTaskErrCodeLLMNotConfigured   = "2000722"
	parseTaskErrCodeNgNames            = "2000723"
	parseTaskErrCodeNoNodes            = "2000724"
	parseTaskErrCodeMilvusUnavailable  = "2000725"
	parseTaskErrCodeVectorPool         = "2000726"
	parseTaskErrCodeParserSubmit       = "2000727"
	parseTaskErrCodeReparseFailed      = "2000728"
	parseTaskErrCodeServiceUnavailable = "2000729"
	parseTaskErrCodeTimeout            = "2000730"
)

const defaultParseTaskErrCode = parseTaskErrCodeSubmitFailed

type parseTaskErrorRule struct {
	keyword string
	code    string
}

var parseTaskErrorRules = []parseTaskErrorRule{
	{keyword: "rate limiting", code: parseTaskErrCodeRateLimit},
	{keyword: "rpm limit", code: parseTaskErrCodeRateLimit},
	{keyword: "no source is configured for dynamic llm", code: parseTaskErrCodeLLMNotConfigured},
	{keyword: "ng_names", code: parseTaskErrCodeNgNames},
	{keyword: "has no nodes for docs", code: parseTaskErrCodeNoNodes},
	{keyword: "fail connecting to server on milvus", code: parseTaskErrCodeMilvusUnavailable},
	{keyword: "_client_pool", code: parseTaskErrCodeVectorPool},
	{keyword: "parser_submit_failed", code: parseTaskErrCodeParserSubmit},
	{keyword: "execute reparse task failed", code: parseTaskErrCodeReparseFailed},
	{keyword: "connection refused", code: parseTaskErrCodeServiceUnavailable},
	{keyword: "timeout", code: parseTaskErrCodeTimeout},
}

// legacyParseTaskErrText maps previously persisted Chinese messages to error codes.
var legacyParseTaskErrText = map[string]string{
	"任务提交失败":                   parseTaskErrCodeSubmitFailed,
	"API 调用频率超限，请稍后重试":         parseTaskErrCodeRateLimit,
	"LLM 模型未配置":                parseTaskErrCodeLLMNotConfigured,
	"解析组不存在或未注册":               parseTaskErrCodeNgNames,
	"依赖切片未生成，请先完成基础解析":         parseTaskErrCodeNoNodes,
	"Milvus 连接失败，请检查向量库服务是否可用": parseTaskErrCodeMilvusUnavailable,
	"向量库连接异常":                  parseTaskErrCodeVectorPool,
	"解析任务提交失败":                 parseTaskErrCodeParserSubmit,
	"解析执行失败":                   parseTaskErrCodeReparseFailed,
	"解析服务不可用":                  parseTaskErrCodeServiceUnavailable,
	"解析超时":                     parseTaskErrCodeTimeout,
}

func isParseTaskErrCode(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) == 7 && strings.HasPrefix(s, "200072")
}

func extractParseTaskErrorMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "{") {
		return raw
	}
	var payload struct {
		Message string `json:"message"`
		Msg     string `json:"msg"`
		Error   string `json:"error"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return raw
	}
	for _, s := range []string{payload.Message, payload.Msg, payload.Error, payload.Detail} {
		if text := strings.TrimSpace(s); text != "" {
			return text
		}
	}
	return raw
}

// mapParseTaskError maps upstream/raw errors to a core error code for frontend i18n.
func mapParseTaskError(raw string) string {
	msg := extractParseTaskErrorMessage(raw)
	if msg == "" {
		return ""
	}
	if isParseTaskErrCode(msg) {
		return msg
	}
	if code, ok := legacyParseTaskErrText[msg]; ok {
		return code
	}
	lower := strings.ToLower(msg)
	for _, rule := range parseTaskErrorRules {
		if strings.Contains(lower, rule.keyword) {
			return rule.code
		}
	}
	return msg
}
