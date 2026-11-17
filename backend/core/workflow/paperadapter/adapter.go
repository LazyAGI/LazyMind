// Package paperadapter owns paper-specific tool authorization, context and budgets.
package paperadapter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflowtool"
	"net/http"
	"strings"
)

const Binding = `"""Audited paper primitives; the executing Agent owns all generation and judgment."""
from lazymind.document_tools.paper.agent_tools import (
    paper_import_sources,
    paper_analyze, paper_plot, paper_prepare_candidate, paper_prepare_inputs,
    paper_execution_status, paper_cancel_execution, paper_preview_revision,
    paper_knowledge_documents, paper_knowledge_read,
    paper_read_material, paper_read_paper, paper_run_experiment, paper_search,
    paper_submit_candidate, paper_read_result,
)
`

var legacyBinding = strings.Replace(Binding, "    paper_import_sources,\n", "", 1)

var toolNames = map[string]bool{
	"paper_import_sources": true,
	"paper_prepare_inputs": true, "paper_read_material": true, "paper_search": true, "paper_read_paper": true,
	"paper_knowledge_documents": true, "paper_knowledge_read": true, "paper_analyze": true, "paper_plot": true,
	"paper_run_experiment": true, "paper_prepare_candidate": true, "paper_execution_status": true,
	"paper_cancel_execution": true, "paper_preview_revision": true,
}

var steps = map[string]bool{
	"research_topic": true, "literature_review": true, "refine_topic": true, "experiment": true,
	"data_analysis": true, "write_outline": true, "write_document": true, "peer_review": true,
	"revise_document": true, "reading_reflection": true, "finish": true,
}

type Adapter struct {
	DB            *gorm.DB
	ReadKnowledge func(context.Context, string, executor.AttemptContext, string, map[string]any) (workflowtool.Result, error)
}

func (a *Adapter) Prepare(ctx context.Context, contract executor.AttemptContext, input workflowtool.CallInput) (map[string]any, error) {
	if !executor.UsesPaperAgentContract(contract) || !steps[contract.StepID] || !toolNames[input.ToolName] {
		return nil, controlstore.Reject("TOOL_NOT_ALLOWED", "Tool does not belong to the pinned paper adapter")
	}
	allowed := false
	for _, tool := range contract.Execution.Tools {
		if tool.Name == input.ToolName && tool.Source == "package" {
			allowed = true
		}
	}
	if !allowed {
		return nil, controlstore.Reject("TOOL_NOT_ALLOWED", "Paper tool is not declared by this step")
	}
	if _, supplied := input.Arguments["__paper_context"]; supplied {
		return nil, controlstore.Reject("INVALID_ARGUMENT", "Caller cannot supply service-owned paper context")
	}
	var entry orm.WorkflowRevisionEntry
	var blob orm.WorkflowBlob
	if err := a.DB.WithContext(ctx).Where("revision_id = ? AND path = ? AND entry_type = ?", contract.WorkflowRevision, "scripts/agent_tools.py", "file").Take(&entry).Error; err != nil || entry.BlobHash == nil || entry.Binary {
		return nil, controlstore.Reject("TOOL_NOT_ALLOWED", "Pinned paper binding is unavailable")
	}
	if err := a.DB.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil || blob.Binary ||
		(string(blob.Content) != Binding && (input.ToolName == "paper_import_sources" || string(blob.Content) != legacyBinding)) || fmt.Sprintf("%x", sha256.Sum256(blob.Content)) != *entry.BlobHash {
		return nil, controlstore.Reject("TOOL_NOT_ALLOWED", "Pinned paper binding is not an audited adapter")
	}
	return executor.PaperToolContext(ctx, a.DB, contract)
}

func reply(w http.ResponseWriter, status int, result any, failure *workflowtool.ToolError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		ContractVersion string                  `json:"contract_version"`
		OK              bool                    `json:"ok"`
		Result          any                     `json:"result,omitempty"`
		Error           *workflowtool.ToolError `json:"error,omitempty"`
	}{"workflow.v1", failure == nil, result, failure})
}

func serviceError(w http.ResponseWriter, err error) {
	var tool *workflowtool.ToolError
	var controlled *controlstore.Error
	if errors.As(err, &tool) {
		reply(w, http.StatusConflict, nil, tool)
		return
	}
	if errors.As(err, &controlled) {
		reply(w, controlled.HTTPStatus(), nil, &workflowtool.ToolError{Code: controlled.Code, Message: controlled.Message})
		return
	}
	reply(w, http.StatusServiceUnavailable, nil, &workflowtool.ToolError{Code: "WORKFLOW_TOOL_FAILED", Message: "Paper data service is unavailable", Retryable: true})
}
