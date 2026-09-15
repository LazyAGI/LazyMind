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
// ordinary Markdown drafts can create the default provider document on first delivery.
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
		if (slot.SlotID != "draft_document" && slot.SlotID != "flat_draft_document") || slot.ListIndex != nil {
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
		if !hasBinding && writerArtifactIsUnboundIR(sourceValue) {
			binding, hasBinding = writerProviderBindingFromArtifact(draftValue)
			if !hasBinding {
				info.State = writerWriteBackInitialDelivery
			}
		}
		if !hasBinding {
			if writerArtifactIsMarkdown(draftValue) {
				info.State = writerWriteBackInitialDelivery
			}
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
		Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision < ?", sessionID, draft.SlotID, draft.Revision).
		Order("revision DESC").
		Find(&revisions).Error; err != nil {
		return info
	}
	for _, revision := range revisions {
		value, err := LoadSlotRevisionValue(ctx, db, revision)
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
	info.URL = writerProviderURL(binding.URI)
}

func writerRevisionPointer(revision int) *int {
	return &revision
}

func loadWriterSlotDTOValue(ctx context.Context, db *gorm.DB, sessionID string, slot slotDTO) (json.RawMessage, error) {
	return LoadSlotRevisionValue(ctx, db, orm.WorkflowSlotRevision{
		SessionID:       sessionID,
		ArtifactSeq:     slot.ArtifactSeq,
		HumanArtifactID: slot.HumanArtifactID,
		ContentSnapshot: slot.ContentSnapshot,
		Slot:            slot.Slot,
		StepID:          slot.StepID,
		Attempt:         slot.Attempt,
	})
}

func writerSlotRevisionSynced(changeSource string, value json.RawMessage) bool {
	if changeSource == "provider_sync" {
		return true
	}
	if changeSource != "ai" && changeSource != "host" {
		return false
	}
	resolved, ok := resolveWriterArtifact(value)
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
	resolved, ok := resolveWriterArtifact(value)
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
	if json.Unmarshal(document, &identity) != nil ||
		!writerProviderSupported(identity.ProviderBinding.Provider) ||
		identity.ProviderBinding.DocumentID == "" {
		return writerProviderBinding{}, false
	}
	return identity.ProviderBinding, true
}

func resolveWriterArtifact(value json.RawMessage) (json.RawMessage, bool) {
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
	return content, true
}

func writerArtifactIsMarkdown(value json.RawMessage) bool {
	var artifact struct {
		Schema string          `json:"schema"`
		Data   json.RawMessage `json:"data"`
		Path   string          `json:"path"`
	}
	if json.Unmarshal(value, &artifact) != nil {
		return false
	}
	if artifact.Schema == "text/markdown" {
		var markdown string
		if json.Unmarshal(artifact.Data, &markdown) == nil && strings.TrimSpace(markdown) != "" {
			return true
		}
	}
	if artifact.Path == "" {
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

func writerArtifactIsUnboundIR(value json.RawMessage) bool {
	resolved, ok := resolveWriterArtifact(value)
	if !ok {
		return false
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(resolved, &envelope) != nil {
		return false
	}
	document := resolved
	if len(envelope.Data) > 0 {
		document = envelope.Data
	}
	var valueDocument struct {
		DocumentID      string         `json:"document_id"`
		ProviderBinding map[string]any `json:"provider_binding"`
	}
	return json.Unmarshal(document, &valueDocument) == nil && valueDocument.DocumentID != "" &&
		len(valueDocument.ProviderBinding) == 0
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

func writerProviderSupported(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "feishu", "notion":
		return true
	default:
		return false
	}
}

func writerProviderURL(uri string) string {
	if !strings.HasPrefix(uri, "https://") {
		return ""
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(uri, "https://"), "/")[0])
	if host == "feishu.cn" || strings.HasSuffix(host, ".feishu.cn") ||
		host == "larksuite.com" || strings.HasSuffix(host, ".larksuite.com") ||
		host == "app.notion.com" ||
		host == "notion.so" || strings.HasSuffix(host, ".notion.so") ||
		host == "notion.site" || strings.HasSuffix(host, ".notion.site") {
		return uri
	}
	return ""
}
