package common

import (
	"net/http"
	"strings"
)

func init() {
	for _, group := range []struct {
		code, status int
		message      string
		sources      []string
	}{
		{2005400, http.StatusInternalServerError, "Unable to load AI reading preferences or progress", []string{"读取 AI 阅读准备设置失败", "读取准备状态失败", "读取准备设置失败"}},
		{2005401, http.StatusBadRequest, "Invalid AI reading preparation settings", []string{"准备设置格式无效", "只能提交一份准备设置", "请选择有效的准备预设", "请提供所有阅读功能的准备方式", "%s 准备方式无效"}},
		{2005402, http.StatusBadRequest, "Select a supported preparation scope", []string{"请选择本篇需要准备的功能", "准备请求格式无效", "本篇准备仅支持 PDF 解析、公式、图片和表格", "准备范围重复"}},
		{2005403, http.StatusUnprocessableEntity, "Select an available document parser in AI reading preferences", []string{"文档解析服务不可用", "文档解析服务凭据不可用", "所选文档解析服务不可用，请在 AI 阅读准备中重新选择"}},
		{2005404, http.StatusUnprocessableEntity, "Select an available study model in AI reading preferences", []string{"研读模型不可用", "所选研读模型不可用，请在 AI 阅读准备中重新选择", "所选研读模型不可用，请在 AI 阅读准备中重新选择或配置。"}},
		{2005405, http.StatusForbidden, "The reading preparation account or permission is unavailable", []string{"阅读准备账户不可用", "阅读准备权限已撤销"}},
		{2005406, http.StatusConflict, "The original document is unavailable or changed; prepare its current version", []string{"原始文档不可用", "原始 PDF 已变化，请重新准备"}},
		{2005407, http.StatusConflict, "Automatic reading preparation stopped because its policy changed", []string{"自动准备策略已变化，本篇不再符合条件", "%s自动准备已停止"}},
		{2005408, http.StatusTooManyRequests, "The daily automatic reading preparation limit has been reached", []string{"已达到每日自动准备文档上限"}},
		{2005409, http.StatusBadRequest, "Preparation limits must be 1–100 items/documents and automatic concurrency must be 1", []string{"无效的研读数量上限", "每日自动文档和每篇研读数量须为 1–100；自动并发目前固定为 1"}},
		{2005410, http.StatusBadGateway, "Reading preparation failed; existing results are preserved", []string{"结构化解析失败", "%s 准备失败", "structured task response contract mismatch"}},
		{2005411, http.StatusConflict, "The PDF structure is not ready", []string{"PDF 结构尚未就绪"}},
		{2005412, http.StatusBadRequest, "Automatic study requires automatic PDF structure parsing", []string{"自动研读需要自动准备结构化 PDF 解析，请先将解析设为自动或关闭自动研读"}},
		{2005413, http.StatusForbidden, "New generation is disabled in AI reading preferences; existing results remain available", []string{"已禁止新的%s准备；已有结果仍可查看，请在 AI 阅读准备设置中修改"}},
	} {
		for _, source := range group.sources {
			if strings.Contains(source, "%") {
				registerAdditionalErrorPattern(source, group.message, group.status, group.code)
			} else {
				registerAdditionalErrorAlias(source, group.message, group.status, group.code)
			}
		}
	}
}
