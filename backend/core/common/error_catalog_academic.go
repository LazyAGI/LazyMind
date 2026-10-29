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
		{2005100, http.StatusForbidden, "Dataset write permission is required", []string{"dataset write permission required"}},
		{2005101, http.StatusBadRequest, "Library kind must be paper or document", []string{"library_kind must be paper or document"}},
		{2005102, http.StatusConflict, "Library kind cannot be changed after it is set", []string{"library kind cannot be changed after it is set"}},
		{2005103, http.StatusBadRequest, "Invalid arXiv candidate version", []string{"arxiv candidate has invalid version %q"}},
		{2005104, http.StatusBadRequest, "The arXiv URL and candidate specify different versions", []string{"arxiv version conflict"}},
		{2005105, http.StatusBadGateway, "The arXiv PDF redirect is not an official HTTPS PDF URL", []string{
			"arxiv pdf redirect must use an official https pdf url",
			"arxiv pdf redirect points outside the official arxiv hosts",
		}},
		{2005106, http.StatusBadGateway, "The arXiv PDF redirect changed the paper or version", []string{"arxiv pdf redirect changed the paper or version"}},
		{2005107, http.StatusBadGateway, "arXiv metadata lookup failed", []string{
			"arxiv request failed", "arxiv lookup failed", "arxiv lookup returned http %d", "arxiv response is invalid",
		}},
		{2005108, http.StatusBadGateway, "arXiv metadata is missing a verified identifier, version, or title", []string{"arxiv 返回的题录缺少可靠的编号、版本或标题"}},
		{2005109, http.StatusBadGateway, "Failed to download the arXiv PDF", []string{"arxiv pdf 下载失败：%w"}},
		{2005110, http.StatusInternalServerError, "Failed to store or confirm the imported arXiv document", []string{
			"arxiv 文件入库失败：%w", "无法确认已保存文献：%w",
		}},
		{2005111, http.StatusInternalServerError, "Paper processing did not start or failed; retry in document tasks", []string{
			"文献处理任务未能启动",
			"pdf 已保存，但处理任务未能启动，请重试",
			"pdf 已保存，但处理未成功，请在文档任务中重试：%w",
			"pdf 已保存，但处理未成功，请在文档任务中重试：%s",
			"pdf 已在库中，但处理失败，请在文档任务中重试：%s",
		}},
		{2005112, http.StatusInternalServerError, "The imported paper file is unavailable or damaged", []string{
			"导入未产生可读取的 pdf 附件，请重试", "文献 %s 的文件信息损坏：%w", "imported document file is unavailable",
		}},
		{2005113, http.StatusInternalServerError, "Failed to read the saved paper metadata", []string{"无法读取已保存文献元数据：%w"}},
		{2005114, http.StatusInternalServerError, "The file was saved but its recovery marker failed; retry the import", []string{"文件已保存，恢复标记保存失败，请重试导入：%w"}},
		{2005115, http.StatusInternalServerError, "The PDF was saved but its paper association failed; retry the import", []string{"pdf 已保存，文献关联保存失败，请重新导入以恢复：%w"}},
		{2005116, http.StatusInternalServerError, "Paper processing status is unavailable", []string{
			"无法读取文献解析任务状态：%w", "文献解析状态数据库未连接",
		}},
		{2005117, http.StatusInternalServerError, "Unable to coordinate the arXiv import; retry after checking the database", []string{
			"arxiv import database lock was lost",
			"arxiv import locking does not support database %q",
			"arxiv import locking requires a postgresql connection dsn",
			"configure arxiv import lock", "connect arxiv import lock",
			"arxiv import locking requires a local sqlite proxy",
			"arxiv import locking requires a local sqlite proxy and shared database filesystem",
			"arxiv import locking requires a file-backed sqlite database",
			"arxiv import locking requires access to the sqlite database filesystem",
		}},
		{2005118, http.StatusInternalServerError, "Failed to save the interrupted paper import status", []string{"save interrupted import failure"}},
		{2005119, http.StatusInternalServerError, "Some paper imports failed; inspect their errors before retrying", []string{"%d paper import items failed; inspect item errors before retrying"}},
		{2005120, http.StatusInternalServerError, "Paper import did not produce a document", []string{"paper import did not return a document"}},
		{2005121, http.StatusBadGateway, "Unpaywall open-access lookup failed", []string{
			"unpaywall request failed", "unpaywall lookup failed", "unpaywall lookup returned http %d", "unpaywall response is invalid",
		}},
		{2005122, http.StatusBadGateway, "OpenAlex metadata lookup failed", []string{
			"openalex request failed", "openalex lookup failed", "openalex lookup returned http %d", "openalex response is invalid", "openalex response is missing results",
		}},
		{2005123, http.StatusBadRequest, "Invalid paper library JSON", []string{"extra json value", "论文库 json 无效"}},
		{2005124, http.StatusBadRequest, "Paper library data is empty or exceeds the supported limits", []string{
			"论文库数据为空或过大", "论文库数据嵌套层级或项目数量过多", "论文库单项文本超过 2 mib",
			"论文库列表超过 50000 项", "论文库单项字段过多", "论文库字段名称过长", "单个论文库最多支持 10000 篇论文",
			"library snapshot envelope too large", "too many %s", "paper metadata too large", "too many papers",
		}},
		{2005125, http.StatusBadRequest, "Invalid paper library data", []string{
			"论文库 schemaversion 必须为 3", "论文库 %s 必须为数组", "论文库 %s 项目必须为对象",
			"论文库 %s 的 id 为空、重复或过长", "论文 important 必须为布尔值", "论文 year 无效",
			"论文 metadata 必须为对象", "论文 metadata.%s 必须为对象", "论文 folderid 无效", "论文 %s 无效",
			"论文附件 contentref 无效", "论文库 %s.%s 必须为文本", "论文库 %s 必须为文本数组",
			"目录 parentid 无效", "目录名称不能为空", "同一目录下不能存在同名目录", "上级目录不存在",
			"目录层级不能包含循环", "论文所属目录不存在", "论文附件引用无效", "同一 pdf 不能关联到多篇论文",
			"invalid or duplicate %s identity", "duplicate folder identity", "library snapshot must be an object", "paper folder does not exist",
		}},
		{2005126, http.StatusInternalServerError, "Failed to read the imported document file", []string{"read imported file metadata", "read imported file"}},
		{2005127, http.StatusInternalServerError, "Failed to recover the imported document task", []string{"recover imported document task"}},
		{2005128, http.StatusInternalServerError, "Failed to read the imported document task metadata", []string{"read imported task metadata"}},
		{2005137, 409, "The paper content or cache is unavailable or changed; retry the read", []string{"cache identity mismatch", "legacy fulltext job requires a new expansion-context read", "original pdf changed before caching", "paper source context required", "pdf version changed", "pdf yielded no full text", "verified versioned pdf source required"}},
		{2005136, 409, "The paper identifiers conflict", []string{"conflicting paper identifiers", "work identity conflict", "paper identifiers conflict"}},
		{2005133, 409, "The selected paper source is unavailable or changed; start a new task", []string{"expansion paper is not a saved seed or verified external candidate", "handoff paper identity is missing", "paper has no frozen source in this handoff", "paper is not in this handoff", "paper must be a saved seed or an externally discovered candidate of this expansion", "selected document identity changed; start a new task", "selected pdf changed"}},
		{2005131, 409, "The import preview expired or changed; refresh and retry", []string{"参考文献与导入预览不一致，请重新确认", "导入预览已过期或不属于当前用户和论文集，请刷新预览", "导入预览已过期，请刷新预览后重试", "论文或版本与导入预览不一致，请重新确认"}},
		{2005134, 409, "The paper workflow input is not ready or changed; refresh and retry", []string{"academic knowledge base is not accessible", "chat launch returned http %d", "expansion requires selected directions", "paper_id is required", "request was deleted; start a new request", "request_key belongs to a different expansion version or paper selection", "selected direction is not a candidate", "unsupported expansion discovery mode", "user instruction missing", "心得与已保存的上一步产物不一致", "所选拓展版本不可用", "所选拓展版本尚未完成", "所选论文不属于当前扩展成果，请重新选择", "所选论文全文暂不可用，请刷新后重试", "拓展尚未完成", "至少两篇不同论文全文解析成功后才能生成综述", "请至少选择两篇全文可用论文", "阅读心得尚未完成"}},
		{2005135, 400, "The selected paper does not match the import request", []string{"doi 服务未返回匹配的有效题录", "doi 查询失败（http %d）", "downloadable papers must use the import queue", "import request does not match its original papers", "import request key is already in use", "imported paper is missing from the saved library", "linked paper does not match the import preview", "请输入 doi 或 arxiv 编号、链接"}},
		{2005119, 500, "Some paper imports failed; inspect their errors before retrying", []string{"one or more paper imports failed", "verified arxiv metadata required"}},
		{2005132, 400, "The paper upload request is invalid or conflicts with an existing request", []string{"paper upload only supports pdf", "paper upload request does not match its original context", "paper upload request key is already in use", "paper upload requires a paper library", "paper upload requires an uploaded pdf", "record paper upload"}},
		{2005130, 400, "Paper libraries require document parsing", []string{"academic paper libraries require at least parsed processing", "enable document parsing before using an academic paper library"}},
		{2005138, http.StatusConflict, "The paper library changed; refresh and retry", []string{"论文已在其他窗口更新，请保留草稿并加载最新内容后重试"}},
		{2005139, http.StatusInternalServerError, "Failed to read the paper library data", []string{"paper %s has no scholarly identity"}},
		{2005140, http.StatusConflict, "Processing submission is unconfirmed; verify the existing task before retrying", []string{
			"imported task metadata is unavailable; submission outcome requires verification",
			"previous processing submission outcome requires verification",
			"remote processing submission outcome requires verification",
			"processing submission outcome requires verification",
			"the previous processing submission is not confirmed; reconcile the task before retrying",
			"the previous processing task state is unknown; reconcile it before retrying",
		}},
		{2005141, http.StatusBadRequest, "The target is not a paper library", []string{"target is not a paper library"}},
		{2005142, http.StatusConflict, "An imported note or attachment ID already belongs to another paper", []string{
			"note identity belongs to another paper", "attachment identity belongs to another paper",
		}},
		{2005143, http.StatusBadRequest, "An imported attachment must reference a document in this library", []string{
			"attachment belongs to another dataset", "new attachment must reference an existing document",
		}},
		{2005144, http.StatusConflict, "A legacy document belongs to multiple papers; resolve its memberships before retrying", []string{
			"legacy document has multiple memberships; original archive retained for explicit resolution",
		}},
		{2005145, http.StatusInternalServerError, "Failed to preserve the legacy paper library", []string{"preserve legacy library %s"}},
		{2005147, http.StatusBadGateway, "Unable to find an available paper PDF source", []string{"pdf 来源查询失败", "PDF 来源查询失败: %s", "一次只能查找一篇文献的 PDF 来源"}},
		{2005146, http.StatusConflict, "A paper folder with this name already exists", []string{"folder name already exists"}},
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
