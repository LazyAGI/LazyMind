package common

import "testing"

func TestResolveAcademicImportErrorsPreservesContext(t *testing.T) {
	for _, sample := range []struct {
		name, source string
		status, code int
		message      string
		detail       any
	}{
		{"dataset permission", "dataset write permission required", 403, 2004900, "Dataset write permission is required", nil},
		{"candidate version", `arXiv candidate has invalid version "latest"`, 400, 2004903, "Invalid arXiv candidate version", `arXiv candidate has invalid version "latest"`},
		{"provider failure", "OpenAlex lookup failed: upstream connection: timeout", 502, 2004922, "OpenAlex metadata lookup failed", "upstream connection: timeout"},
		{"download failure", "arXiv PDF 下载失败：context deadline exceeded", 504, 2004909, "Failed to download the arXiv PDF", "arXiv PDF 下载失败：context deadline exceeded"},
		{"submission failure", "文献处理任务未能启动: parser rejected the document", 500, 2004911, "Paper processing did not start or failed; retry in document tasks", "parser rejected the document"},
		{"library validation", "论文库 papers 的 id 为空、重复或过长", 400, 2004925, "Invalid paper library data", "论文库 papers 的 id 为空、重复或过长"},
		{"folder validation", "目录层级不能包含循环", 400, 2004925, "Invalid paper library data", nil},
		{"library revision conflict", "paper library revision conflict", 409, 2004938, "The paper library changed; refresh and retry", nil},
		{"library read failure", "read paper graph: invalid JSON", 500, 2004939, "Failed to read the paper library data", "invalid JSON"},
		{"saved task recovery", "recover imported document task: database is locked", 500, 2004927, "Failed to recover the imported document task", "database is locked"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			resolved := ResolveAppError(sample.source, sample.status)
			if resolved.Code != sample.code || resolved.HTTPStatus != sample.status || resolved.Message != sample.message || resolved.Detail != sample.detail {
				t.Fatalf("resolved error = %#v; want code=%d status=%d message=%q detail=%#v", resolved, sample.code, sample.status, sample.message, sample.detail)
			}
		})
	}
}
