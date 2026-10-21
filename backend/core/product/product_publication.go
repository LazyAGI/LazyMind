package product

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/publication"
)

// publishedProductArtifacts resolves the last complete delivery, including after
// working revisions invalidate the old graph selection. Exact IDs, not selected
// flags, define a publication. Authorization is still checked for every read.
func (r *Repository) publishedProductArtifacts(ctx context.Context, owner string, session orm.WorkflowSession) ([]Artifact, error) {
	pub, err := publication.Latest(r.db.WithContext(ctx), session.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Artifact, 0, len(pub.Revisions))
	for slot, id := range pub.Revisions {
		value, err := r.ReadArtifact(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		if value.SessionID != session.ID || value.SlotID != slot {
			return nil, repositoryError("PRODUCT_PUBLICATION_INVALID")
		}
		if slot == "workspace_state" || slot == "stage_manifest" {
			var row orm.WorkflowSlotRevision
			if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
				return nil, err
			}
			raw, err := publication.Bytes(r.db.WithContext(ctx), row)
			if err != nil {
				return nil, err
			}
			value.Value = raw
		}
		// An invalidated working dependency does not erase its immutable publication.
		value.Validity = "effective"
		result = append(result, value)
	}
	return result, nil
}

func (r *Repository) productDrafts(ctx context.Context, owner string, session orm.WorkflowSession, published []Artifact) ([]map[string]any, error) {
	current, err := r.ListArtifacts(ctx, owner, session.ID)
	if err != nil {
		return nil, err
	}
	pinned := map[string]string{}
	for _, a := range published {
		pinned[a.SlotID] = a.ID
	}
	result := []map[string]any{}
	for _, a := range current {
		if a.Validity != "effective" || a.ID == pinned[a.SlotID] {
			continue
		}
		for _, stage := range productStages {
			if a.SlotID == stage.MarkdownSlot || a.SlotID == stage.HTMLSlot {
				result = append(result, map[string]any{"stage": stage.ID, "slot_id": a.SlotID, "revision_id": a.ID, "revision": a.Revision, "status": "draft"})
			}
		}
	}
	return result, nil
}
