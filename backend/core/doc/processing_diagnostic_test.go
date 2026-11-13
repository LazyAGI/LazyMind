package doc

import (
	"lazymind/core/common/orm"
	"strings"
	"testing"
)

func TestSegmentWriteDiagnostic(t *testing.T) {
	raw := "[_DocumentStore] Failed to upsert segment data for group lazyllm_root"
	d := processingDiagnostic(orm.DocumentProcessingState{}, orm.Task{ID: "task-test"}, "FAILED", "", raw)
	if d == nil || !strings.Contains(d.Message, "正文存储失败") || !strings.Contains(d.Message, "重新解析") || d.TaskID != "task-test" {
		t.Fatalf("unexpected diagnostic: %+v", d)
	}
	if processingDiagnostic(orm.DocumentProcessingState{}, orm.Task{}, "SUCCESS", "", raw) != nil {
		t.Fatal("successful latest task must not expose an old failure")
	}
}
