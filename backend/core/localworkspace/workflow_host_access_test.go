package localworkspace

import "testing"

func TestWorkflowHostAccessRequiresExactPinnedRegistrySelection(t *testing.T) {
	for _, tc := range []struct {
		name, tool string
		selections []string
		allowed    bool
	}{
		{"writer create", "WriterCreateToolkit_generate_final_document", []string{"writer_create"}, true},
		{"writer revision", "WriterRevisionToolkit_apply_patch", []string{"writer_revision"}, true},
		{"vision registry alias", "vision_extractor", []string{"multimodal"}, true},
		{"image", "image_editor", []string{"image_editor"}, true},
		{"video", "video_generator", []string{"video_generator"}, true},
		{"gif", "video_to_gif", []string{"video_to_gif"}, true},
		{"local fs cannot admit writer", "WriterCreateToolkit_generate_final_document", []string{"local_fs"}, false},
		{"create cannot admit revision", "WriterRevisionToolkit_apply_patch", []string{"writer_create"}, false},
		{"different media selection", "image_editor", []string{"image_generator"}, false},
		{"runtime name is not registry selection", "vision_extractor", []string{"vision_extractor"}, false},
		{"fabricated method", "WriterCreateToolkit_arbitrary_write", []string{"writer_create"}, false},
		{"private method", "WriterCreateToolkit__write_document", []string{"writer_create"}, false},
		{"unexposed inherited method", "WriterCreateToolkit_replace_document", []string{"writer_create"}, false},
		{"unregistered resource toolkit", "WriterResourceToolkit_replace_document", []string{"writer_create", "writer_revision"}, false},
		{"unknown", "Shell_exec", []string{"Shell_exec", "local_fs"}, false},
		{"no tools", "image_editor", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := OperationRequest{ExecutionMode: hostAccessExecutionMode, ToolName: tc.tool}
			if got := workflowOperationToolAllowed(tc.selections, req); got != tc.allowed {
				t.Fatalf("allowed=%v want %v", got, tc.allowed)
			}
		})
	}
	for _, mode := range []string{"", "local"} {
		req := OperationRequest{ExecutionMode: mode, ToolName: "read"}
		if workflowOperationToolAllowed([]string{"local_fs"}, req) {
			t.Fatal("obsolete local mode admitted")
		}
		if workflowOperationToolAllowed([]string{"writer_create"}, req) {
			t.Fatal("local mode escaped pinned tools")
		}
	}
}

func TestHostAccessSensitiveAndExternalPolicyAcrossModes(t *testing.T) {
	for _, mode := range []string{PermissionAlwaysAsk, PermissionAskAsNeeded, PermissionAllowAll} {
		t.Run(mode, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, mode)
			for _, operation := range []OperationKind{OperationRead, OperationWrite, OperationDelete} {
				req := hostRequest(grant, conversation, "plain.bin", operation)
				req.Path = t.TempDir() + "/nonexistent/file.bin"
				req.CallID = operationTestCallID("outside-" + string(operation))
				result, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{req}})
				want := DecisionPending
				if err != nil || result.Operations[0].Decision != want {
					t.Fatalf("external %s: %+v %v", operation, result, err)
				}
			}
			for _, operation := range []OperationKind{OperationWrite, OperationDelete} {
				req := hostRequest(grant, conversation, ".env", operation)
				if err := validateOperationRequest(req); err != nil {
					t.Fatalf("named path %s rejected: %v", operation, err)
				}
			}
			req := hostRequest(grant, conversation, ".env", OperationRead)
			req.CallID = operationTestCallID("sensitive")
			result, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{req}})
			want := DecisionPending
			if err != nil || result.Operations[0].Decision != want {
				t.Fatalf("sensitive read %+v %v want %s", result, err, want)
			}
		})
	}
}
