package academic

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/doc"
)

type importFailureDetails struct {
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
	Detail   string `json:"detail,omitempty"`
	Recovery string `json:"recovery,omitempty"`
}

var importHTTPStatus = regexp.MustCompile(`(?i)\bHTTP\s+(\d{3})\b`)

func importFailure(err error, stage string, saved bool) importFailureDetails {
	raw := err.Error()
	text := strings.ToLower(raw)
	d := importFailureDetails{Code: "import_failed", Message: "系统处理论文来源信息时出错", Detail: doc.SafeProcessingMessage(raw), Recovery: "请重试；若持续失败，请提供错误码和任务编号排查。"}
	status := ""
	if match := importHTTPStatus.FindStringSubmatch(raw); len(match) > 1 {
		status = match[1]
	}
	var lookup *arxivsource.LookupError
	if errors.As(err, &lookup) && lookup.StatusCode != 0 {
		status = fmt.Sprint(lookup.StatusCode)
	}
	switch {
	case status == "429":
		d.Code, d.Message, d.Recovery = "provider_rate_limited", "论文来源请求受限", "请稍后重试。"
	case status == "401" || status == "403" || strings.Contains(text, "api key"):
		d.Code, d.Message, d.Recovery = "provider_authorization_failed", "论文来源授权不足", "请检查来源的 API Key 或文件访问权限后重试。"
	case status == "404":
		d.Code, d.Message, d.Recovery = "source_not_found", "来源未找到该论文文件", "请重新查询或选择有效的全文来源。"
	case strings.HasPrefix(status, "5") || strings.Contains(text, "deadline") || strings.Contains(text, "timeout") || strings.Contains(text, "timed out"):
		d.Code, d.Message, d.Recovery = "provider_unavailable", "论文来源暂不可用或请求超时", "请稍后重试。"
	case strings.Contains(text, "identity conflict") || strings.Contains(text, "snapshot") || strings.Contains(text, "version conflict") || strings.Contains(text, "content changed"):
		d.Code, d.Message, d.Recovery = "import_source_mismatch", "论文来源信息与确认内容不一致", "请重新查询并核对论文和版本后导入。"
	case strings.Contains(text, "pdf") || strings.Contains(text, "mime") || strings.Contains(text, "size limit"):
		d.Code, d.Message, d.Recovery = "invalid_original", "来源未返回可用的 PDF 文件", "请检查原文件，或重新选择全文来源。"
	case stage == "save":
		d.Code, d.Message = "import_save_failed", "论文保存失败"
	case saved:
		d.Code, d.Message, d.Recovery = "processing_failed", "正文处理失败", "可重试正文处理，无需重新上传 PDF。"
	case stage == "resolve":
		d.Code, d.Message = "resolution_failed", "论文题录查询失败"
	case stage == "download":
		d.Code, d.Message = "download_failed", "论文文件下载失败"
	}
	if saved {
		d.Message = "PDF 已保存，" + d.Message
	} else {
		d.Message += "，论文尚未入库"
	}
	return d
}
