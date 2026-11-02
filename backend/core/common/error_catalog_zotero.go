package common

import (
	"net/http"
	"strings"
)

func init() {
	for _, group := range []struct {
		code    int
		status  int
		message string
		sources []string
	}{
		{2004930, http.StatusBadRequest, "Invalid paper folder hierarchy", []string{
			"论文文件夹 parentid 无效", "论文文件夹的父文件夹不存在",
			"论文文件夹不能包含循环层级", "论文文件夹最多支持 32 层",
			"folder parent does not exist", "folder cycle", "folder depth exceeds 32",
		}},
		{2004931, http.StatusConflict, "The Zotero paper or attachment changed; reload the library", []string{
			"zotero 文献或附件已移除，请重新读取论文库", "zotero 附件不属于目标文献",
			"zotero 附件已在其他窗口更新，请重新读取论文库",
		}},
		{2004932, http.StatusBadRequest, "Invalid Zotero PDF upload; files must be valid PDFs up to 128 MiB", []string{
			"zotero 附件不是有效 pdf 或超过 128 mib", "pdf 上传无效或超过 128 mib",
			"pdf 上传失败或超过 128 mib", "请选择 pdf 附件", "文献或附件标识无效",
		}},
		{2004933, http.StatusInternalServerError, "Failed to store the Zotero attachment", []string{
			"zotero 附件保存失败：%w", "无法暂存 pdf 附件",
		}},
		{2004934, http.StatusConflict, "The library changed; the PDF is saved, retry linking it", []string{
			"论文库正在其他窗口更新，pdf 已保留，请重试关联",
		}},
		{2004935, http.StatusConflict, "The attachment checksum changed; reload Zotero metadata and select the matching file", []string{
			"附件校验值与 zotero 题录不一致，请重新读取题录并选择对应文件",
		}},
		{2004936, http.StatusForbidden, "Local Zotero access requires the authenticated owner of a local desktop runtime", []string{"local zotero access requires the authenticated owner of a local desktop runtime"}},
		{2004937, http.StatusServiceUnavailable, "Local Zotero owner authentication is unavailable", []string{"local zotero owner authentication is unavailable"}},
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
