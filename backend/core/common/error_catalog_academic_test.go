package common

import "testing"

func TestResolveAcademicImportErrorsPreservesContext(t *testing.T) {
	for _, sample := range []struct {
		name, source string
		status, code int
		message      string
		detail       any
	}{
		{"dataset permission", "dataset write permission required", 403, 2005000, "Dataset write permission is required", nil},
		{"candidate version", `arXiv candidate has invalid version "latest"`, 400, 2005003, "Invalid arXiv candidate version", `arXiv candidate has invalid version "latest"`},
		{"provider failure", "OpenAlex lookup failed: upstream connection: timeout", 502, 2005022, "OpenAlex metadata lookup failed", "upstream connection: timeout"},
		{"download failure", "arXiv PDF 下载失败：context deadline exceeded", 504, 2005009, "Failed to download the arXiv PDF", "arXiv PDF 下载失败：context deadline exceeded"},
		{"submission failure", "文献处理任务未能启动: parser rejected the document", 500, 2005011, "Paper processing did not start or failed; retry in document tasks", "parser rejected the document"},
		{"library validation", "论文库 papers 的 id 为空、重复或过长", 400, 2005025, "Invalid paper library data", "论文库 papers 的 id 为空、重复或过长"},
		{"folder validation", "目录层级不能包含循环", 400, 2005025, "Invalid paper library data", nil},
		{"library revision conflict", "论文已在其他窗口更新，请保留草稿并加载最新内容后重试", 409, 2005038, "The paper library changed; refresh and retry", nil},
		{"library read failure", "paper entry-1 has no scholarly identity", 500, 2005039, "Failed to read the paper library data", "paper entry-1 has no scholarly identity"},
		{"saved task recovery", "recover imported document task: database is locked", 500, 2005027, "Failed to recover the imported document task", "database is locked"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			resolved := ResolveAppError(sample.source, sample.status)
			if resolved.Code != sample.code || resolved.HTTPStatus != sample.status || resolved.Message != sample.message || resolved.Detail != sample.detail {
				t.Fatalf("resolved error = %#v; want code=%d status=%d message=%q detail=%#v", resolved, sample.code, sample.status, sample.message, sample.detail)
			}
		})
	}
}
