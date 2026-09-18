package chat

import (
	"context"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"testing"
)

func TestForkHistoryDoesNotDependOnLocalSources(t *testing.T) {
	history := orm.ChatHistory{Content: "context", Result: "answer", Ext: marshalChatHistoryExt(map[string]any{
		"fork_read_only": true, "conversation_config_snapshot": map[string]any{"version": 1, "local_fs_source_ids": []string{"old-source"}},
	})}
	result, err := revalidateForkHistoryAttachments(context.Background(), nil, doc.DatasetCatalogCaller{UserID: "u1"}, []orm.ChatHistory{history})
	if err != nil || len(result) != 1 || result[0].Content != history.Content || result[0].Result != history.Result {
		t.Fatalf("history changed: %#v, %v", result, err)
	}
}
