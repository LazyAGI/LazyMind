package common

import (
	"net/http"
	"strings"
)

func init() {
	// Keep import failures distinct from validation and provider failures. Format
	// templates retain their concrete identifiers and upstream details when the
	// catalog resolves them; academic endpoints also retain their source messages.
	for _, group := range []struct {
		code    int
		status  int
		message string
		sources []string
	}{
		{2004900, http.StatusForbidden, "Dataset write permission is required", []string{"dataset write permission required"}},
		{2004901, http.StatusBadRequest, "Library kind must be paper or document", []string{"library_kind must be paper or document"}},
		{2004902, http.StatusConflict, "Library kind cannot be changed after it is set", []string{"library kind cannot be changed after it is set"}},
		{2004903, http.StatusBadRequest, "Invalid arXiv candidate version", []string{"arxiv candidate has invalid version %q"}},
		{2004904, http.StatusBadRequest, "The arXiv URL and candidate specify different versions", []string{"arxiv version conflict"}},
		{2004905, http.StatusBadGateway, "The arXiv PDF redirect is not an official HTTPS PDF URL", []string{
			"arxiv pdf redirect must use an official https pdf url",
			"arxiv pdf redirect points outside the official arxiv hosts",
		}},
		{2004906, http.StatusBadGateway, "The arXiv PDF redirect changed the paper or version", []string{"arxiv pdf redirect changed the paper or version"}},
		{2004907, http.StatusBadGateway, "arXiv metadata lookup failed", []string{
			"arxiv request failed", "arxiv lookup failed", "arxiv lookup returned http %d", "arxiv response is invalid",
		}},
		{2004908, http.StatusBadGateway, "arXiv metadata is missing a verified identifier, version, or title", []string{"arxiv 返回的题录缺少可靠的编号、版本或标题"}},
		{2004909, http.StatusBadGateway, "Failed to download the arXiv PDF", []string{"arxiv pdf 下载失败：%w"}},
		{2004910, http.StatusInternalServerError, "Failed to store or confirm the imported arXiv document", []string{
			"arxiv 文件入库失败：%w", "无法确认已保存文献：%w",
		}},
		{2004911, http.StatusInternalServerError, "Paper processing did not start or failed; retry in document tasks", []string{
			"文献处理任务未能启动",
			"pdf 已保存，但处理未成功，请在文档任务中重试：%w",
			"pdf 已保存，但处理未成功，请在文档任务中重试：%s",
			"pdf 已在库中，但处理失败，请在文档任务中重试：%s",
		}},
		{2004912, http.StatusInternalServerError, "The imported paper file is unavailable or damaged", []string{
			"导入未产生可读取的 pdf 附件，请重试", "文献 %s 的文件信息损坏：%w", "imported document file is unavailable",
		}},
		{2004913, http.StatusInternalServerError, "Failed to read the saved paper metadata", []string{"无法读取已保存文献元数据：%w"}},
		{2004914, http.StatusInternalServerError, "The file was saved but its recovery marker failed; retry the import", []string{"文件已保存，恢复标记保存失败，请重试导入：%w"}},
		{2004915, http.StatusInternalServerError, "The PDF was saved but its paper association failed; retry the import", []string{"pdf 已保存，文献关联保存失败，请重新导入以恢复：%w"}},
		{2004916, http.StatusInternalServerError, "Paper processing status is unavailable", []string{
			"无法读取文献解析任务状态：%w", "文献解析状态数据库未连接",
		}},
		{2004917, http.StatusInternalServerError, "Unable to coordinate the arXiv import; retry after checking the database", []string{
			"arxiv import database lock was lost",
			"arxiv import locking does not support database %q",
			"arxiv import locking requires a postgresql connection dsn",
			"configure arxiv import lock", "connect arxiv import lock",
			"arxiv import locking requires a local sqlite proxy",
			"arxiv import locking requires a local sqlite proxy and shared database filesystem",
			"arxiv import locking requires a file-backed sqlite database",
			"arxiv import locking requires access to the sqlite database filesystem",
		}},
		{2004918, http.StatusInternalServerError, "Failed to save the interrupted paper import status", []string{"save interrupted import failure"}},
		{2004919, http.StatusInternalServerError, "Some paper imports failed; inspect their errors before retrying", []string{"%d paper import items failed; inspect item errors before retrying"}},
		{2004920, http.StatusInternalServerError, "Paper import did not produce a document", []string{"paper import did not return a document"}},
		{2004921, http.StatusBadGateway, "Unpaywall open-access lookup failed", []string{
			"unpaywall request failed", "unpaywall lookup failed", "unpaywall lookup returned http %d", "unpaywall response is invalid",
		}},
		{2004922, http.StatusBadGateway, "OpenAlex metadata lookup failed", []string{
			"openalex request failed", "openalex lookup failed", "openalex lookup returned http %d", "openalex response is invalid", "openalex response is missing results",
		}},
		{2004923, http.StatusBadRequest, "Invalid paper library JSON", []string{"extra json value", "论文库 json 无效"}},
		{2004924, http.StatusBadRequest, "Paper library data is empty or exceeds the supported limits", []string{
			"论文库数据为空或过大", "论文库数据嵌套层级或项目数量过多", "论文库单项文本超过 2 mib",
			"论文库列表超过 50000 项", "论文库单项字段过多", "论文库字段名称过长", "单个论文库最多支持 10000 篇论文",
		}},
		{2004925, http.StatusBadRequest, "Invalid paper library data", []string{
			"论文库 schemaversion 必须为 3", "论文库 %s 必须为数组", "论文库 %s 项目必须为对象",
			"论文库 %s 的 id 为空、重复或过长", "论文 important 必须为布尔值", "论文 year 无效",
			"论文 metadata 必须为对象", "论文 metadata.%s 必须为对象", "论文 folderid 无效", "论文 %s 无效",
			"论文附件 contentref 无效", "论文库 %s.%s 必须为文本", "论文库 %s 必须为文本数组",
		}},
		{2004926, http.StatusInternalServerError, "Failed to read the imported document file", []string{"read imported file metadata", "read imported file"}},
		{2004927, http.StatusInternalServerError, "Failed to recover the imported document task", []string{"recover imported document task"}},
		{2004928, http.StatusInternalServerError, "Failed to read the imported document task metadata", []string{"read imported task metadata"}},
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
