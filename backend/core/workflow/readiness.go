package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

// WorkflowReadiness is a read-only check of a published revision, never its draft.
// Missing per-turn inputs remain the Workflow's normal clarification flow.
type WorkflowReadiness struct {
	Ready       bool                     `json:"ready"`
	WorkflowRef string                   `json:"workflow_ref"`
	Name        string                   `json:"name"`
	RevisionID  string                   `json:"revision_id"`
	RevisionNo  int64                    `json:"revision_no"`
	Issues      []WorkflowReadinessIssue `json:"issues"`
	RepairURL   string                   `json:"repair_url"`
	Editable    bool                     `json:"editable"`
}
type WorkflowReadinessIssue struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	NodeID    string         `json:"node_id,omitempty"`
	Path      string         `json:"path,omitempty"`
	RepairURL string         `json:"repair_url,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

func CheckWorkflowReadiness(ctx context.Context, db *gorm.DB, userID, ref, revisionID string) (WorkflowReadiness, error) {
	result := WorkflowReadiness{WorkflowRef: ref, Issues: []WorkflowReadinessIssue{}, RepairURL: "/memory-management/skills?skillView=workflows"}
	pkg, err := workflowstore.New(db).GetWorkflowPackage(ctx, userID, ref, revisionID)
	if errors.Is(err, workflowstore.ErrNotFound) {
		result.Issues = append(result.Issues, WorkflowReadinessIssue{Code: "WORKFLOW_NOT_ACCESSIBLE", Message: "找不到可用的已发布工作流，或你没有访问权限。"})
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.WorkflowRef, result.Name, result.RevisionID, result.RevisionNo = pkg.WorkflowRef, pkg.Name, pkg.RevisionID, pkg.RevisionNo
	var resource orm.WorkflowResource
	if err := db.WithContext(ctx).Where("id = ?", pkg.ResourceID).First(&resource).Error; err != nil {
		return result, err
	}
	if resource.OwnerUserID == userID && resource.SourceDraftID != "" {
		var draft orm.WorkflowDraft
		err := db.WithContext(ctx).Select("id").Where("id = ? AND created_by = ? AND deleted_at IS NULL", resource.SourceDraftID, userID).First(&draft).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
		if err == nil {
			result.Editable = true
			result.RepairURL = "/memory-management/workflows/" + url.PathEscape(draft.ID)
		}
	}
	compiled := graphengine.Compile(string(pkg.Files["workflow.yaml"]), string(pkg.Files["scenario/state.yml"]), string(pkg.Files["scenario/scenario.md"]), graphengine.ProfileRuntimeLoad)
	for _, diagnostic := range compiled.Diagnostics {
		if diagnostic.Severity == "error" {
			result.Issues = append(result.Issues, WorkflowReadinessIssue{Code: diagnostic.Code, Message: diagnostic.Message, NodeID: diagnostic.NodeID, Path: diagnostic.Path, RepairURL: result.RepairURL, Details: diagnostic.Details})
		}
	}
	if !compiled.Valid {
		return result, nil
	}
	var graph graphengine.CompiledStateGraph
	if json.Unmarshal(pkg.CompiledGraph, &graph) != nil || graph.Nodes == nil {
		result.Issues = append(result.Issues, WorkflowReadinessIssue{Code: "WORKFLOW_GRAPH_INVALID", Message: "已发布版本缺少有效的执行图，请重新校验并发布。", RepairURL: result.RepairURL})
		return result, nil
	}
	requirements := []skillCapabilityRequirement{}
	declaredCapabilities, declaredTools := map[string]bool{}, map[string]bool{}
	for _, node := range compiled.Graph.Nodes {
		for _, capability := range node.Capabilities {
			declaredCapabilities[capability] = true
		}
		for _, tool := range append(append([]string{}, node.LegacyTools...), node.TerminalTools...) {
			declaredTools[tool] = true
		}
	}
	for _, known := range skillCapabilityCatalog {
		if (workflowCapabilityUsesToolConfig(known.WorkflowCapability) || workflowCapabilityUsesModelConfig(known.WorkflowCapability)) && requiredCapabilityDeclared(known, declaredCapabilities, declaredTools) {
			requirements = append(requirements, known)
		}
	}
	for _, issue := range skillCapabilityRuntimeConfigIssues(ctx, db, userID, requirements) {
		if strings.HasSuffix(issue.Reason, "_check_failed") {
			return result, fmt.Errorf("check workflow runtime configuration: %s", issue.Reason)
		}
		repairURL := "/settings?section=system_tools"
		if workflowCapabilityUsesModelConfig(issue.Capability) {
			repairURL = "/settings?section=models&view=defaults"
		}
		result.Issues = append(result.Issues, WorkflowReadinessIssue{Code: "WORKFLOW_REQUIRED_CONFIG_MISSING", Message: "尚未配置运行所需的" + issue.Label + "。", RepairURL: repairURL})
	}
	result.Ready = len(result.Issues) == 0
	return result, nil
}

type WorkflowNotReadyError struct{ Result WorkflowReadiness }

func (e *WorkflowNotReadyError) Error() string { return "plugin validation failed" }

func RequireWorkflowReady(ctx context.Context, db *gorm.DB, owner, ref, revisionID string) error {
	result, err := CheckWorkflowReadiness(ctx, db, owner, ref, revisionID)
	if err != nil {
		return err
	}
	if !result.Ready {
		return &WorkflowNotReadyError{Result: result}
	}
	return nil
}

func CheckWorkflowInvocationReadiness(ctx context.Context, db *gorm.DB, owner, ref, conversationID string) (WorkflowReadiness, error) {
	revisionID := ""
	if conversationID != "" {
		var session orm.WorkflowSession
		err := db.WithContext(ctx).Where("conversation_id = ? AND create_user_id = ? AND plugin_ref = ? AND dismissed = false", conversationID, owner, ref).Order("created_at DESC").First(&session).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowReadiness{}, err
		}
		if err == nil {
			revisionID = session.WorkflowRevisionID
		}
	}
	return CheckWorkflowReadiness(ctx, db, owner, ref, revisionID)
}

// GetWorkflowReadiness performs no writes, input binding, model calls or launches.
func GetWorkflowReadiness(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("workflow_ref"))
	if ref == "" {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}
	result, err := CheckWorkflowInvocationReadiness(r.Context(), store.DB(), userID, ref, strings.TrimSpace(r.URL.Query().Get("conversation_id")))
	if err != nil {
		common.ReplyErr(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, result)
}
