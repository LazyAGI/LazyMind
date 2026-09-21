package store

import (
	"context"
	"encoding/json"
	"errors"

	"gopkg.in/yaml.v3"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/workflow/artifactgraph"
	"lazymind/core/workflow/document"
)

// DescribeArtifact is an HTTP read projection; internal mutation reads do not
// depend on Algorithm availability. The caller supplies an authorized Artifact.
func (r *Repository) DescribeArtifact(ctx context.Context, owner string, artifact *Artifact, allowSave bool) {
	artifact.Document = nil
	artifact.DocumentError = nil
	var session orm.WorkflowSession
	if err := r.db.WithContext(ctx).Where("id = ?", artifact.SessionID).First(&session).Error; err != nil {
		artifact.DocumentError = document.Unavailable()
		return
	}
	if owner == "" || session.CreateUserID != owner ||
		(ConversationScope(ctx) != "" && ConversationScope(ctx) != session.ConversationID) {
		artifact.DocumentError = document.Unavailable()
		return
	}
	writable := allowSave && artifact.Selected && artifact.Validity == "effective" && DocumentSessionEditable(&session)
	artifact.Document, artifact.DocumentError = document.Project(ctx, artifact.Value, artifact.ContentType, writable, func() (bool, error) {
		return r.PinnedMarkdownHint(ctx, &session, artifact.SlotID)
	}, r.writerDisplayTarget(ctx, &session, artifact))
	if artifact.Document != nil && writable {
		err := artifactgraph.CheckConsumers(ctx, r.db, session.ID, artifact.ID)
		if errors.Is(err, artifactgraph.ErrArtifactInUse) {
			artifact.Document.Editable = false
			artifact.Document.Capabilities = []string{}
		} else if err != nil {
			artifact.Document = nil
			artifact.DocumentError = document.Unavailable()
		}
	}

	if artifact.Document != nil && artifact.Document.Editable {
		config, err := modelconfig.LoadLLMConfig(ctx, r.db, owner)
		if err == nil && RewriteModelAvailable(config) {
			artifact.Document.Capabilities = append(artifact.Document.Capabilities, "rewrite_selection")
		}
	}
	// Conversion, numbering and cross-reference lookup are read-only, so live
	// consumers do not disable them. Mutations still require the live guard.
	if artifact.Document != nil && writable {
		artifact.Document.Capabilities = append(artifact.Document.Capabilities, "convert_document", "numbering", "cross_reference")
		if artifact.Document.Editable && ArtifactPublicationAllowed(session.WorkflowID, artifact.SlotID, artifact.ListIndex) {
			artifact.Document.Capabilities = append(artifact.Document.Capabilities, "publish_document")
		}
	}
}

// ArtifactPublicationAllowed limits Writer delivery to single draft artifacts.
// Other workflows retain generic document publication.
func ArtifactPublicationAllowed(workflowID, slotID string, listIndex *int) bool {
	return workflowID != "writer-workflow" || listIndex == nil && (slotID == "draft_document" || slotID == "flat_draft_document")
}

// Imported Writer Markdown uses a separate target artifact until first publish.
// This mirrors the existing shared Writer target, for display only. Historical
// and unrelated artifacts do not inherit the currently selected source.
func (r *Repository) writerDisplayTarget(ctx context.Context, session *orm.WorkflowSession, artifact *Artifact) json.RawMessage {
	if session.WorkflowID != "writer-workflow" || !artifact.Selected || artifact.Validity != "effective" || artifact.ListIndex != nil {
		return nil
	}
	if artifact.SlotID != "source_document" && artifact.SlotID != "draft_document" && artifact.SlotID != "flat_draft_document" {
		return nil
	}
	var target orm.WorkflowSlotRevision
	if err := r.db.WithContext(ctx).Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ?", session.ID, "target_document", true, "effective").First(&target).Error; err != nil {
		return nil
	}
	raw, ct, _, _, err := r.resolveArtifact(ctx, target)
	if err != nil {
		return nil
	}
	resolved, err := document.ReadArtifactValue(raw, ct)
	if err != nil {
		return nil
	}
	return document.ArtifactData(resolved)
}

func DocumentSessionEditable(session *orm.WorkflowSession) bool {
	if session.Dismissed {
		return false
	}
	switch session.Status {
	case "active", "waiting", "completed", "failed":
		return true
	default:
		return false
	}
}

func RewriteModelAvailable(config map[string]any) bool {
	llm, ok := config["llm"].(map[string]any)
	model, _ := llm["model"].(string)
	return ok && model != ""
}

// PinnedMarkdownHint reads only the immutable revision associated with this
// authorized Session; it does not select an Action or grant write permission.
func (r *Repository) PinnedMarkdownHint(ctx context.Context, session *orm.WorkflowSession, slotID string) (bool, error) {
	if session.WorkflowRevisionID == "" {
		return false, nil
	}
	ref := session.WorkflowRef
	if ref == "" {
		ref = session.WorkflowID
	}
	pkg, err := r.GetWorkflowPackage(ctx, session.CreateUserID, ref, session.WorkflowRevisionID)
	if err != nil {
		return false, err
	}
	var manifest struct {
		UI struct {
			Slots map[string]struct {
				WidgetType string `yaml:"widgetType"`
			} `yaml:"slots"`
		} `yaml:"ui"`
	}
	if err := yaml.Unmarshal(pkg.Files["workflow.yaml"], &manifest); err != nil {
		return false, err
	}
	return manifest.UI.Slots[slotID].WidgetType == "text-markdown", nil
}
