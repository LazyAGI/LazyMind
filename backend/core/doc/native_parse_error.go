package doc

import (
	"encoding/json"
	"errors"
	"net/http"

	"lazymind/core/common"
)

// Only explicit content codes from the native reader may cross the API boundary.
// Upstream text can contain local paths or credentials and is never a UI message.
type nativeParseError struct {
	code    string
	message string
	content bool
	cause   error
}

func (e *nativeParseError) Error() string { return e.message }
func (e *nativeParseError) Unwrap() error { return e.cause }
func classifyNativeParseError(err error) *nativeParseError {
	result := &nativeParseError{code: "READER_SUBMIT_FAILED", message: "文档解析服务暂时不可用，请稍后重试。", cause: err}
	var upstream *common.HTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusUnprocessableEntity {
		return result
	}
	var body struct {
		Detail struct {
			Code string `json:"code"`
		} `json:"detail"`
	}
	if json.Unmarshal(upstream.Body, &body) != nil {
		return result
	}
	switch body.Detail.Code {
	case "EMPTY_DOCUMENT":
		result.code = body.Detail.Code
		result.message = "文档无可读正文，请补充内容后重新上传。"
		result.content = true
	case "OCR_REQUIRED":
		result.code = body.Detail.Code
		result.message = "文档没有可提取的文字，扫描件或图片需要 OCR；原文件已保留，请配置 OCR 后重试。"
		result.content = true
	}
	return result
}
