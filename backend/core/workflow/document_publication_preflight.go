package workflow

import (
	"context"
	"encoding/json"
	"net/http"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	corestore "lazymind/core/store"
	workflowstore "lazymind/core/workflow/store"
)

type PublicationVault struct {
	ID       string `json:"id" required:"true"`
	Name     string `json:"name" required:"true"`
	Writable bool   `json:"writable" required:"true"`
}

type PublicationTargetCheck struct {
	Provider       string             `json:"provider" required:"true"`
	State          string             `json:"state" required:"true"`
	Code           string             `json:"code,omitempty"`
	TargetDocument json.RawMessage    `json:"target_document,omitempty"`
	Vaults         []PublicationVault `json:"vaults" required:"true"`
}

type PublicationPreflightResult struct {
	Targets []PublicationTargetCheck `json:"targets" required:"true"`
}

func publicationTargetErrorCode(code string) string {
	switch code {
	case "OBSIDIAN_NOT_CONFIGURED", "OBSIDIAN_VAULT_REQUIRED", "OBSIDIAN_PATH_REQUIRED", "OBSIDIAN_PATH_INVALID", "OBSIDIAN_PERMISSION_REQUIRED", "OBSIDIAN_TARGET_EXISTS", "OBSIDIAN_TARGET_CHANGED", "OBSIDIAN_BASELINE_REQUIRED", "OBSIDIAN_ATTACHMENT_UNAVAILABLE", "PUBLICATION_TARGET_CHANGED", "PUBLICATION_TARGET_MISSING", "PUBLICATION_TARGET_EXISTS", "PUBLICATION_PERMISSION_REQUIRED":
		return code
	default:
		return "PUBLICATION_TARGET_UNAVAILABLE"
	}
}

func checkPublicationTarget(ctx context.Context, owner, provider, connection, title, parent string, target json.RawMessage) (*PublicationTargetCheck, error) {
	config, err := modelconfig.LoadWriterProviderConnectionConfig(ctx, provider, owner, connection)
	if err != nil {
		return nil, documentFailure("PROVIDER_CREDENTIALS_UNAVAILABLE", 422)
	}
	args := map[string]any{"provider": provider, "title": title, "parent_uri": parent}
	if len(target) > 0 {
		args["target_document"] = target
	}
	response, _, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{Reference: "builtin:document.publication_target.v1", Phase: "preview", Arguments: args, ToolConfig: config})
	if err != nil {
		return nil, documentFailure("DOCUMENT_PROVIDERS_UNAVAILABLE", 502)
	}
	result := &PublicationTargetCheck{Provider: provider, Vaults: []PublicationVault{}}
	if json.Unmarshal(response.Result, result) != nil || result.State == "" {
		return nil, documentFailure("DOCUMENT_PROVIDERS_RESULT_INVALID", 502)
	}
	result.Provider = provider
	switch result.State {
	case "ready", "blocked", "permission_required", "not_configured":
	default:
		return nil, documentFailure("DOCUMENT_PROVIDERS_RESULT_INVALID", 502)
	}
	if result.Code != "" {
		result.Code = publicationTargetErrorCode(result.Code)
	}
	return result, nil
}

func ReadObsidianPublicationStatus(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	result, err := checkPublicationTarget(r.Context(), owner, "obsidian", "", "", "", nil)
	if err != nil {
		replyDocumentFailure(w, publicationFailure(err))
		return
	}
	common.ReplyOK(w, result)
}

func PreflightDocumentPublication(ctx context.Context, db *gorm.DB, owner, artifactID string, request DocumentPublicationGroupRequest) (*PublicationPreflightResult, error) {
	if len(request.Targets) == 0 || len(request.Targets) > 6 {
		return nil, publicationError("DOCUMENT_ACTION_INVALID")
	}
	var revision orm.WorkflowSlotRevision
	if err := db.First(&revision, "id = ?", artifactID).Error; err != nil {
		return nil, publicationError("PUBLICATION_NOT_FOUND")
	}
	session, err := publicationSession(ctx, db, owner, revision.SessionID)
	if err != nil {
		return nil, err
	}
	if !workflowstore.ArtifactPublicationAllowed(session.WorkflowID, revision.SlotID, revision.ListIndex) {
		return nil, publicationError("DOCUMENT_ACTION_INVALID")
	}
	_, content, _, _, err := publicationSource(ctx, db, DocumentPublicationInput{SessionID: revision.SessionID, SlotID: revision.SlotID, ListIndex: revision.ListIndex, BaseRevision: request.BaseRevision, BaseDraftVersion: request.BaseDraftVersion}, artifactID)
	if err != nil {
		return nil, err
	}
	result := &PublicationPreflightResult{Targets: []PublicationTargetCheck{}}
	seen := map[string]bool{}
	for _, input := range request.Targets {
		if !validPublicationTarget(input) || seen[input.Provider] {
			return nil, publicationError("DOCUMENT_ACTION_INVALID")
		}
		seen[input.Provider] = true
		if input.Provider != "obsidian" && input.Provider != "github" {
			continue
		}
		var binding orm.DocumentPublicationBinding
		query := db.Where("session_id = ? AND slot_id = ? AND item_index = ? AND owner_user_id = ? AND binding_scope = ?", revision.SessionID, revision.SlotID, publicationIndex(revision.ListIndex), owner, input.Provider)
		if err := query.Find(&binding).Error; err != nil {
			return nil, err
		}
		target := binding.TargetDocument
		if len(target) == 0 {
			inherited, _, err := initialPublicationTarget(ctx, db, &DocumentPublicationOperation{PublicationID: "preflight", SessionID: revision.SessionID, SlotID: revision.SlotID, ItemIndex: publicationIndex(revision.ListIndex), SourceRevision: revision.Revision}, revision)
			if err != nil {
				return nil, err
			}
			if targetProvider(inherited) == input.Provider {
				target = inherited
			}
		}
		config, err := modelconfig.LoadWriterProviderConnectionConfig(ctx, input.Provider, owner, input.ConnectionID)
		if err != nil {
			return nil, documentFailure("PROVIDER_CREDENTIALS_UNAVAILABLE", 422)
		}
		artifact, _ := json.Marshal(map[string]any{"data": content.Value})
		converted, _, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{Reference: documentConvertReference, Phase: "preview", Artifact: artifact, Arguments: map[string]any{"provider": input.Provider}, ToolConfig: config})
		if err != nil {
			return nil, documentFailure("DOCUMENT_CONVERSION_FAILED", 502)
		}
		var source struct {
			Source struct {
				Title string `json:"title"`
			} `json:"source_document"`
		}
		if json.Unmarshal(converted.Result, &source) != nil {
			return nil, documentFailure("DOCUMENT_CONVERSION_FAILED", 502)
		}
		title := input.Title
		if title == "" {
			title = source.Source.Title
		}
		checked, err := checkPublicationTarget(ctx, owner, input.Provider, input.ConnectionID, title, input.ParentURI, target)
		if err != nil {
			return nil, err
		}
		// Public checks expose readiness, not internal file paths or document metadata.
		checked.TargetDocument = nil
		result.Targets = append(result.Targets, *checked)
	}
	return result, nil
}

func PreflightDocumentPublicationHTTP(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	var request DocumentPublicationGroupRequest
	if decodeDocumentJSON(r.Body, &request) != nil {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	result, err := PreflightDocumentPublication(r.Context(), corestore.DB(), owner, common.PathVar(r, "artifact_id"), request)
	if err != nil {
		replyDocumentFailure(w, publicationFailure(err))
		return
	}
	common.ReplyOK(w, result)
}
