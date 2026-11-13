package executor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

// ExecutionContract is the same revision-owned instruction set for native and
// external executors. The surrounding AttemptContext owns the single prompt,
// acceptance criteria and output declaration.
type ExecutionContract struct {
	graphengine.StepExecution
	SkillFiles []SkillFile `json:"skill_files,omitempty"`
}

type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// Skills are resolved from the same immutable revision as the step, never HEAD
// or caller-controlled disk paths. Deliver text with the contract so an external
// worker does not need another host's filesystem to follow its instructions.
func loadExecutionSkillFiles(ctx context.Context, db *gorm.DB, value *AttemptContext) error {
	if value.Execution == nil {
		return nil
	}
	files, err := LoadRevisionSkillFiles(ctx, db, value.WorkflowRevision, value.Execution.Skills)
	if err != nil {
		return err
	}

	value.Execution.SkillFiles = files
	return nil
}

// LoadRevisionSkillFiles shares pinned Skill loading between step execution and
// post-completion artifact edits.
func LoadRevisionSkillFiles(ctx context.Context, db *gorm.DB, revisionID string, skills []string) ([]SkillFile, error) {
	remaining := 512 << 10
	files := make([]SkillFile, 0, len(skills))
	for _, skill := range skills {
		if !graphengine.ValidExecutionSkillPath(skill) {
			return nil, controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "invalid revision skill path")
		}
		var entry orm.WorkflowRevisionEntry
		if err := db.WithContext(ctx).Where("revision_id = ? AND path = ? AND entry_type = ?", revisionID, skill, "file").First(&entry).Error; err != nil || entry.BlobHash == nil {
			return nil, controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill is unavailable: "+skill)
		}
		var blob orm.WorkflowBlob
		if err := db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).First(&blob).Error; err != nil {
			return nil, controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill content is unavailable: "+skill)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(blob.Content))
		if digest != *entry.BlobHash {
			return nil, controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill content hash does not match: "+skill)
		}
		if entry.Binary || blob.Binary || len(blob.Content) > 256<<10 || len(blob.Content) > remaining || !utf8.Valid(blob.Content) {
			return nil, controlstore.Reject("EXTERNAL_SKILL_UNAVAILABLE", "revision skill must be bounded UTF-8 text: "+skill)
		}
		remaining -= len(blob.Content)
		files = append(files, SkillFile{
			Path: skill, Content: string(blob.Content), SHA256: "sha256:" + digest,
		})
	}
	return files, nil
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
		if value.Execution == nil || !value.Execution.Supported() || strings.TrimSpace(value.Prompt) == "" {
			return controlstore.Reject("EXTERNAL_EXECUTION_REQUIRED", "revision does not declare a supported execution contract")
		}
		value.Metadata = nil
		return nil
	})
	return value, err
}

// The caller supplies a tool name, never its provider or script path. Source is
// resolved from the pinned contract and cannot be elevated through arguments.
func AuthorizeExternalToolCall(ctx context.Context, db *gorm.DB, owner, sessionID, attemptID, handle, name string) (AttemptContext, graphengine.ExecutionTool, error) {
	value, err := AuthorizeExternalExecution(ctx, db, owner, sessionID, attemptID, handle)
	if err != nil {
		return AttemptContext{}, graphengine.ExecutionTool{}, err
	}
	for _, tool := range value.Execution.Tools {
		if tool.Name == name {
			return value, tool, nil
		}
	}
	return AttemptContext{}, graphengine.ExecutionTool{}, controlstore.Reject("EXTERNAL_TOOL_UNDECLARED", "tool is not declared by this step")
}
