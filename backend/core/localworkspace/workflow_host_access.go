package localworkspace

import "slices"

// workflowOperationToolAllowed maps deployed runtime leaf names to the pinned
// registry selections. Never trust a request-supplied group or a class prefix:
// only explicitly exported methods are admitted. Keep these names in sync with
// tool_registry.py and the Writer toolkit __public_apis__ declarations.
func workflowOperationToolAllowed(tools []string, req OperationRequest) bool {
	if req.ExecutionMode != hostAccessExecutionMode {
		return false
	}
	var selection string
	switch req.ToolName {
	case "read", "write", "edit", "ls", "glob", "grep", "mkdir", "move", "remove", "stat":
		return slices.Contains(tools, req.ToolName)
	case "WriterCreateToolkit_build_writing_task",
		"WriterCreateToolkit_build_resources",
		"WriterCreateToolkit_profile_resources",
		"WriterCreateToolkit_create_writing_context",
		"WriterCreateToolkit_prepare_outline",
		"WriterCreateToolkit_generate_outline",
		"WriterCreateToolkit_generate_rewrite_outline",
		"WriterCreateToolkit_generate_rewrite_section_instructions",
		"WriterCreateToolkit_generate_section_instructions",
		"WriterCreateToolkit_generate_draft_section",
		"WriterCreateToolkit_generate_draft_section_markdown",
		"WriterCreateToolkit_generate_draft_blocks",
		"WriterCreateToolkit_generate_draft_blocks_markdown",
		"WriterCreateToolkit_generate_draft_document",
		"WriterCreateToolkit_generate_draft_document_markdown",
		"WriterCreateToolkit_update_writing_context",
		"WriterCreateToolkit_check_consistency",
		"WriterCreateToolkit_generate_final_document",
		"WriterCreateToolkit_render_markdown":
		selection = "writer_create"
	case "WriterRevisionToolkit_build_revise_task",
		"WriterRevisionToolkit_build_revision_task",
		"WriterRevisionToolkit_locate_revision_target",
		"WriterRevisionToolkit_generate_modify_plan",
		"WriterRevisionToolkit_build_revision_visual_plan",
		"WriterRevisionToolkit_generate_patch_set",
		"WriterRevisionToolkit_generate_string_replace_set",
		"WriterRevisionToolkit_plan_revision",
		"WriterRevisionToolkit_validate_patch_set",
		"WriterRevisionToolkit_apply_patch",
		"WriterRevisionToolkit_apply_string_replace",
		"WriterRevisionToolkit_apply_revision":
		selection = "writer_revision"
	case "vision_extractor":
		selection = "multimodal"
	case "image_generator":
		selection = "image_generator"
	case "image_editor":
		selection = "image_editor"
	case "video_generator":
		selection = "video_generator"
	case "video_to_gif":
		selection = "video_to_gif"
	default:
		return false
	}
	return slices.Contains(tools, selection)
}
