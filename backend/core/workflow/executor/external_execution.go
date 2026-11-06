package executor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

type ExternalExecutionContract struct {
	graphengine.ExternalStepExecution
	SkillFiles        []ExternalSkillFile `json:"skill_files,omitempty"`
	TaskSpecification string              `json:"task_specification,omitempty"`
}

type ExternalSkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// ApplyExternalExecution selects instructions without native tool-only modes or
// automatic generators. Inputs and output types remain the shared data contract.
func ApplyExternalExecution(value *AttemptContext, node graphengine.CompiledNode) {
	if !node.ExternalExecution.Supported() {
		return
	}
	value.ExternalExecution = &ExternalExecutionContract{
		ExternalStepExecution: *node.ExternalExecution,
		// Retain detailed business requirements, enums and output shapes without
		// enabling native wrappers. External prompt/constraints govern execution.
		TaskSpecification: node.Prompt,
	}
	value.Prompt = node.ExternalExecution.Prompt
	value.LegacyTools, value.TerminalTools = nil, nil
	value.ToolsOnly, value.TerminalToolsOnly = false, false
	if node.ExternalExecution.Acceptance != nil {
		value.Acceptance = node.ExternalExecution.Acceptance
	}
	if node.ExternalExecution.RequiredOutputs != nil {
		value.RequiredOutputs = node.ExternalExecution.RequiredOutputs
	}
}

// Skills are resolved from the same immutable revision as the step, never HEAD
// or caller-controlled disk paths. Deliver text with the contract so an external
// worker does not need another host's filesystem to follow its instructions.
func loadExternalSkillFiles(ctx context.Context, db *gorm.DB, value *AttemptContext) error {
	if value.ExternalExecution == nil {
		return nil
	}
	remaining := 512 << 10
	for _, skill := range value.ExternalExecution.Skills {
		if !graphengine.ValidExternalSkillPath(skill) {
			return controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "invalid revision skill path")
		}
		var entry orm.WorkflowRevisionEntry
		if err := db.WithContext(ctx).Where("revision_id = ? AND path = ? AND entry_type = ?", value.WorkflowRevision, skill, "file").First(&entry).Error; err != nil || entry.BlobHash == nil {
			return controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill is unavailable: "+skill)
		}
		var blob orm.WorkflowBlob
		if err := db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).First(&blob).Error; err != nil {
			return controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill content is unavailable: "+skill)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(blob.Content))
		if digest != *entry.BlobHash {
			return controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill content hash does not match: "+skill)
		}
		if entry.Binary || blob.Binary || len(blob.Content) > 256<<10 || len(blob.Content) > remaining || !utf8.Valid(blob.Content) {
			return controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill must be bounded UTF-8 text: "+skill)
		}
		remaining -= len(blob.Content)
		value.ExternalExecution.SkillFiles = append(value.ExternalExecution.SkillFiles, ExternalSkillFile{
			Path: skill, Content: string(blob.Content), SHA256: "sha256:" + digest,
		})
	}
	return nil
}

// AuthorizeExternalExecution is shared by tool discovery and calls. Call again
// after a long tool operation before publishing its result; admission does not
// give an expired or stopped worker continuing authority.
func AuthorizeExternalExecution(ctx context.Context, db *gorm.DB, owner, sessionID, attemptID, handle string) (AttemptContext, error) {
	if owner == "" {
		return AttemptContext{}, workflowstore.ErrPermissionDenied
	}
	if err := workflowstore.New(db).AuthorizeSession(ctx, sessionID, owner); err != nil {
		return AttemptContext{}, err
	}
	var value AttemptContext
	err := controlstore.Transaction(ctx, db, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if !controlstore.Controlled(*session) {
			return controlstore.Reject("EXTERNAL_EXECUTION_REQUIRED", "tools require a controlled external execution")
		}
		if err := controlstore.ValidateExecution(tx, *session, attemptID, handle); err != nil {
			return err
		}
		var row orm.WorkflowSessionStep
		if err := tx.Where("id = ? AND session_id = ?", attemptID, sessionID).First(&row).Error; err != nil {
			return err
		}
		if row.ExecutorHost != "external-agent" {
			return controlstore.Reject("EXECUTOR_MISMATCH", "external execution does not own this step")
		}
		var err error
		value, err = (DBContextLoader{DB: tx}).LoadAttemptContext(ctx, attemptID)
		if err != nil {
			return err
		}
		if value.ExternalExecution == nil || !value.ExternalExecution.Supported() {
			return controlstore.Reject("EXTERNAL_EXECUTION_REQUIRED", "revision does not declare external execution")
		}
		value.Metadata = nil
		return nil
	})
	return value, err
}

// The caller supplies a tool name, never its provider or script path. Source is
// resolved from the pinned contract and cannot be elevated through arguments.
func AuthorizeExternalToolCall(ctx context.Context, db *gorm.DB, owner, sessionID, attemptID, handle, name string) (AttemptContext, graphengine.ExternalTool, error) {
	value, err := AuthorizeExternalExecution(ctx, db, owner, sessionID, attemptID, handle)
	if err != nil {
		return AttemptContext{}, graphengine.ExternalTool{}, err
	}
	for _, tool := range value.ExternalExecution.Tools {
		if tool.Name == name {
			return value, tool, nil
		}
	}
	return AttemptContext{}, graphengine.ExternalTool{}, controlstore.Reject("EXTERNAL_TOOL_UNDECLARED", "tool is not declared by this step")
}
