package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const (
	writerWriteBackInitialDelivery = "initial_delivery"
	writerWriteBackSyncedClean     = "synced_clean"
	writerWriteBackSyncedDirty     = "synced_dirty"
	writerWriteBackBlocked         = "blocked"
)

type writerProviderBinding struct {
	Provider   string `json:"provider"`
	DocumentID string `json:"document_id"`
	URI        string `json:"uri"`
}

type writerDocumentIdentity struct {
	ProviderBinding writerProviderBinding `json:"provider_binding"`
}

type writerWriteBackInfo struct {
	State              string
	URL                string
	Provider           string
	ProviderDocumentID string
	LastSyncedRevision *int
}

// enrichWriterWriteBackSlots exposes the server-owned delivery state for the
// selected Writer draft. A source_document remains authoritative when present;
// ordinary Markdown drafts can create a Feishu document on first delivery.
func enrichWriterWriteBackSlots(ctx context.Context, db *gorm.DB, sessionID string, slots []slotDTO) {
	var source *slotDTO
	for i := range slots {
		if slots[i].SlotID == "source_document" && slots[i].ListIndex == nil {
			source = &slots[i]
			break
		}
	}

	for i := range slots {
		slot := &slots[i]
		if slot.SlotID != "draft_document" || slot.ListIndex != nil {
			continue
		}
		info := writerWriteBackState(ctx, db, sessionID, *slot, source)
		slot.WriteBackState = info.State
		slot.WriteBackReady = info.State != writerWriteBackBlocked
		slot.WriteBackDirty = info.State == writerWriteBackInitialDelivery || info.State == writerWriteBackSyncedDirty
		slot.WriteBackURL = info.URL
		slot.Provider = info.Provider
		slot.ProviderDocumentID = info.ProviderDocumentID
		slot.LastSyncedRevision = info.LastSyncedRevision
	}
}

func writerWriteBackState(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	draft slotDTO,
	source *slotDTO,
) writerWriteBackInfo {
	info := writerWriteBackInfo{State: writerWriteBackBlocked}
	if draft.Revision <= 0 {
		return info
	}

	draftValue, err := loadWriterSlotDTOValue(ctx, db, sessionID, draft)
	if err != nil {
		return info
	}

	var binding writerProviderBinding
	hasBinding := false
	if source != nil {
		sourceValue, sourceErr := loadWriterSlotDTOValue(ctx, db, sessionID, *source)
		if sourceErr != nil {
			return info
		}
		binding, hasBinding = writerProviderBindingFromArtifact(sourceValue)
		if !hasBinding {
			return info
		}
	} else {
		binding, hasBinding = writerProviderBindingFromArtifact(draftValue)
	}
	if hasBinding {
		applyWriterProviderBinding(&info, binding)
	}

	if writerSlotRevisionSynced(draft.ChangeSource, draftValue) {
		if !hasBinding {
			return info
		}
		info.State = writerWriteBackSyncedClean
		info.LastSyncedRevision = writerRevisionPointer(draft.Revision)
		return info
	}

	var revisions []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision < ?", sessionID, "draft_document", draft.Revision).
		Order("revision DESC").
		Find(&revisions).Error; err != nil {
		return info
	}
	for _, revision := range revisions {
		value, err := loadWriterSlotRevisionValue(ctx, db, revision)
		if err == nil && writerSlotRevisionSynced(revision.ChangeSource, value) {
			if !hasBinding {
				binding, hasBinding = writerProviderBindingFromArtifact(value)
				if hasBinding {
					applyWriterProviderBinding(&info, binding)
				}
			}
			if !hasBinding {
				return info
			}
			info.State = writerWriteBackSyncedDirty
			info.LastSyncedRevision = writerRevisionPointer(revision.Revision)
			return info
		}
	}
	if hasBinding || writerArtifactIsMarkdown(draftValue) {
		info.State = writerWriteBackInitialDelivery
	}
	return info
}

func applyWriterProviderBinding(info *writerWriteBackInfo, binding writerProviderBinding) {
	info.Provider = binding.Provider
	info.ProviderDocumentID = binding.DocumentID
	info.URL = writerFeishuURL(binding.URI)
}

func writerRevisionPointer(revision int) *int {
	return &revision
}

func loadWriterSlotDTOValue(ctx context.Context, db *gorm.DB, sessionID string, slot slotDTO) (json.RawMessage, error) {
	return loadWriterSlotRevisionValue(ctx, db, orm.WorkflowSlotRevision{
		SessionID:       sessionID,
		ArtifactSeq:     slot.ArtifactSeq,
		HumanArtifactID: slot.HumanArtifactID,
		ContentSnapshot: slot.ContentSnapshot,
		Slot:            slot.Slot,
		StepID:          slot.StepID,
		Attempt:         slot.Attempt,
	})
}

func loadWriterSlotRevisionValue(ctx context.Context, db *gorm.DB, revision orm.WorkflowSlotRevision) (json.RawMessage, error) {
	if revision.HumanArtifactID != nil {
		var artifact orm.WorkflowHumanArtifact
		if err := db.WithContext(ctx).Where("id = ?", *revision.HumanArtifactID).First(&artifact).Error; err != nil {
			return nil, err
		}
		return artifact.Value, nil
	}
	if revision.ArtifactSeq != nil {
		var step orm.WorkflowSessionStep
		if err := db.WithContext(ctx).
			Where("session_id = ? AND step_id = ? AND attempt = ?", revision.SessionID, revision.StepID, revision.Attempt).
			First(&step).Error; err != nil {
			return nil, err
		}
		var artifact orm.SubAgentArtifact
		if err := db.WithContext(ctx).
			Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?", step.TaskID, revision.Slot, *revision.ArtifactSeq, false).
			First(&artifact).Error; err != nil {
			return nil, err
		}
		return artifact.Value, nil
	}
	if len(revision.ContentSnapshot) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return revision.ContentSnapshot, nil
}

func writerSlotRevisionSynced(changeSource string, value json.RawMessage) bool {
	if changeSource == "provider_sync" {
		return true
	}
	if changeSource != "ai" {
		return false
	}
	resolved, ok := resolveWriterArtifact(value, 0)
	if !ok {
		return false
	}
	var envelope struct {
		Meta struct {
			ProviderSync struct {
				Confirmed bool `json:"confirmed"`
			} `json:"lazymind_provider_sync"`
		} `json:"meta"`
	}
	return json.Unmarshal(resolved, &envelope) == nil && envelope.Meta.ProviderSync.Confirmed
}

func writerProviderBindingFromArtifact(value json.RawMessage) (writerProviderBinding, bool) {
	resolved, ok := resolveWriterArtifact(value, 0)
	if !ok {
		return writerProviderBinding{}, false
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(resolved, &envelope) != nil {
		return writerProviderBinding{}, false
	}
	document := resolved
	if len(envelope.Data) > 0 {
		document = envelope.Data
	}
	var identity writerDocumentIdentity
	if json.Unmarshal(document, &identity) != nil || identity.ProviderBinding.Provider != "feishu" || identity.ProviderBinding.DocumentID == "" {
		return writerProviderBinding{}, false
	}
	return identity.ProviderBinding, true
}

func resolveWriterArtifact(value json.RawMessage, depth int) (json.RawMessage, bool) {
	if depth > 2 {
		return nil, false
	}
	var record struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(value, &record) != nil || record.Path == "" {
		return value, len(value) > 0
	}
	path := filepath.Clean(record.Path)
	if !writerArtifactPathAllowed(path) {
		return nil, false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return resolveWriterArtifact(content, depth+1)
}

func writerArtifactIsMarkdown(value json.RawMessage) bool {
	var artifact struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(value, &artifact) != nil || artifact.Path == "" {
		return false
	}
	path := filepath.Clean(artifact.Path)
	extension := strings.ToLower(filepath.Ext(path))
	if !writerArtifactPathAllowed(path) || (extension != ".md" && extension != ".markdown") {
		return false
	}
	content, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(content)) != ""
}

func writerArtifactPathAllowed(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for _, root := range []string{os.Getenv("LAZYMIND_SUBAGENT_WORKSPACE"), "/var/lib/lazymind/uploads"} {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func writerFeishuURL(uri string) string {
	if !strings.HasPrefix(uri, "https://") {
		return ""
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(uri, "https://"), "/")[0])
	if host == "feishu.cn" || strings.HasSuffix(host, ".feishu.cn") || host == "larksuite.com" || strings.HasSuffix(host, ".larksuite.com") {
		return uri
	}
	return ""
}
