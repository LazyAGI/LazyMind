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
	writerMarkdownSourceEditor     = "writer-markdown-source"
)

type writerProviderBinding struct {
	Provider   string `json:"provider"`
	DocumentID string `json:"document_id"`
	URI        string `json:"uri"`
	BrowserURL string `json:"browser_url"`
	LocalPath  string `json:"local_path"`
}

type writerDocumentIdentity struct {
	ProviderBinding writerProviderBinding `json:"provider_binding"`
}

type writerWriteBackInfo struct {
	State              string
	URL                string
	LocalPath          string
	Provider           string
	ProviderDocumentID string
	LastSyncedRevision *int
}

// enrichWriterWriteBackSlots exposes the server-owned delivery state for the
// selected Writer draft. Provider-bound Markdown uses target_document, while
// Writer IR continues to use its source or draft provider binding.
func enrichWriterWriteBackSlots(ctx context.Context, db *gorm.DB, sessionID string, slots []slotDTO) {
	var source *slotDTO
	var target *slotDTO
	for i := range slots {
		if !slots[i].Selected {
			continue
		}
		if slots[i].SlotID == "source_document" && slots[i].ListIndex == nil {
			source = &slots[i]
		}
		if slots[i].SlotID == "target_document" && slots[i].ListIndex == nil {
			target = &slots[i]
		}
	}
	if source != nil && target != nil && isWriterWorkflowSession(ctx, db, sessionID) {
		targetValue, err := loadWriterSlotDTOValue(ctx, db, sessionID, *target)
		if err == nil {
			_, bound := writerProviderBindingFromTargetArtifact(targetValue)
			if bound {
				sourceValue, sourceErr := loadWriterSlotDTOValue(ctx, db, sessionID, *source)
				if sourceErr == nil && writerArtifactIsMarkdown(sourceValue, source.ContentType) {
					source.EditorProfile = writerMarkdownSourceEditor
				}
			}
		}
	}

	for i := range slots {
		slot := &slots[i]
		if (slot.SlotID != "draft_document" && slot.SlotID != "flat_draft_document") || slot.ListIndex != nil {
			continue
		}
		info := writerWriteBackState(ctx, db, sessionID, *slot, source, target)
		slot.WriteBackState = info.State
		slot.WriteBackReady = info.State != writerWriteBackBlocked
		slot.WriteBackDirty = info.State == writerWriteBackInitialDelivery || info.State == writerWriteBackSyncedDirty
		slot.WriteBackURL = info.URL
		slot.WriteBackLocalPath = info.LocalPath
		slot.Provider = info.Provider
		slot.ProviderDocumentID = info.ProviderDocumentID
		slot.LastSyncedRevision = info.LastSyncedRevision
	}
}

func isWriterWorkflowSession(ctx context.Context, db *gorm.DB, sessionID string) bool {
	var session orm.WorkflowSession
	return db.WithContext(ctx).
		Select("plugin_id"). // workflow-naming: persistence
		Where("id = ?", sessionID).
		First(&session).Error == nil && session.WorkflowID == "writer-workflow"
}

func writerWriteBackState(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	draft slotDTO,
	source *slotDTO,
	target *slotDTO,
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
	draftIsMarkdown := writerArtifactIsMarkdown(draftValue, draft.ContentType)
	if !draftIsMarkdown {
		binding, hasBinding = writerProviderBindingFromArtifact(draftValue)
	}
	if !hasBinding && draftIsMarkdown && target != nil {
		targetValue, targetErr := loadWriterSlotDTOValue(ctx, db, sessionID, *target)
		if targetErr != nil {
			return info
		}
		binding, hasBinding = writerProviderBindingFromTargetArtifact(targetValue)
	}
	sourceIsUnboundIR := false
	if !hasBinding && source != nil {
		sourceValue, sourceErr := loadWriterSlotDTOValue(ctx, db, sessionID, *source)
		if sourceErr != nil {
			return info
		}
		binding, hasBinding = writerProviderBindingFromArtifact(sourceValue)
		if !hasBinding && writerArtifactIsUnboundIR(sourceValue) {
			sourceIsUnboundIR = true
			binding, hasBinding = writerProviderBindingFromArtifact(draftValue)
		}
	} else if !hasBinding && source == nil {
		binding, hasBinding = writerProviderBindingFromArtifact(draftValue)
	}
	if !hasBinding && source != nil {
		if sourceIsUnboundIR || draftIsMarkdown {
			info.State = writerWriteBackInitialDelivery
		}
		return info
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
	if hasBinding || draftIsMarkdown {
		info.State = writerWriteBackInitialDelivery
	}
	return info
}

func applyWriterProviderBinding(info *writerWriteBackInfo, binding writerProviderBinding) {
	info.Provider = binding.Provider
	info.ProviderDocumentID = binding.DocumentID
	info.LocalPath = binding.LocalPath
	url := binding.BrowserURL
	if strings.TrimSpace(url) == "" {
		url = binding.URI
	}
	info.URL = writerProviderURL(binding.Provider, url)
	if info.URL == "" {
		info.URL = writerInternalTargetURL(canonicalWriterWriteBackProvider(binding.Provider), binding.URI, binding.DocumentID)
	}
	if canonicalWriterWriteBackProvider(binding.Provider) == "obsidian" {
		info.URL = obsidianOpenURL(binding.LocalPath)
	}
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

func writerProviderBindingFromTargetArtifact(value json.RawMessage) (writerProviderBinding, bool) {
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
	target := resolved
	if len(envelope.Data) > 0 {
		target = envelope.Data
	}
	var identity struct {
		DocumentID string `json:"doc_id"`
		URI        string `json:"uri"`
		Adapter    string `json:"adapter"`
		Meta       struct {
			BrowserURL     string `json:"browser_url"`
			PullRequestURL string `json:"pull_request_url"`
			LocalPath      string `json:"local_path"`
		} `json:"meta"`
	}
	if json.Unmarshal(target, &identity) != nil {
		return writerProviderBinding{}, false
	}
	provider := canonicalWriterWriteBackProvider(identity.Adapter)
	if provider == "" || (identity.DocumentID == "" && identity.URI == "") {
		return writerProviderBinding{}, false
	}
	documentID := identity.DocumentID
	if documentID == "" {
		documentID = identity.URI
	}
	uri := identity.Meta.PullRequestURL
	if uri == "" {
		uri = identity.Meta.BrowserURL
	}
	if uri == "" {
		uri = identity.URI
	}
	return writerProviderBinding{
		Provider: provider, DocumentID: documentID, URI: uri,
		LocalPath: identity.Meta.LocalPath,
	}, true
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

func writerArtifactIsMarkdown(value json.RawMessage, contentType string) bool {
	// Editor checkpoints store explicitly typed Markdown as a text carrier.
	if strings.EqualFold(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]), "text/markdown") {
		var text string
		if json.Unmarshal(value, &text) == nil {
			return strings.TrimSpace(text) != ""
		}
		var carrier struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(value, &carrier) == nil && strings.TrimSpace(carrier.Text) != "" {
			return true
		}
	}
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
	switch canonicalWriterWriteBackProvider(provider) {
	case "feishu", "notion", "github", "wechat", "obsidian":
		return true
	default:
		return false
	}
}

func writerProviderURL(provider, uri string) string {
	provider = canonicalWriterWriteBackProvider(provider)
	if internal := writerInternalTargetURL(provider, uri, ""); internal != "" {
		return internal
	}
	if provider == "wechat" && wechatDraftURL(uri) {
		return uri
	}
	if !strings.HasPrefix(uri, "https://") {
		return ""
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(uri, "https://"), "/")[0])
	valid := false
	switch provider {
	case "feishu":
		valid = host == "feishu.cn" || strings.HasSuffix(host, ".feishu.cn") ||
			host == "larksuite.com" || strings.HasSuffix(host, ".larksuite.com")
	case "notion":
		valid = host == "app.notion.com" || host == "notion.so" ||
			strings.HasSuffix(host, ".notion.so") || host == "notion.site" ||
			strings.HasSuffix(host, ".notion.site")
	case "github":
		valid = host == "github.com" || host == "www.github.com"
	case "wechat":
		valid = wechatDraftURL(uri)
	}
	if valid {
		return uri
	}
	return ""
}

func canonicalWriterWriteBackProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "lark", "feishu":
		return "feishu"
	case "github", "githubrepo", "githubwiki":
		return "github"
	case "notion":
		return "notion"
	case "wechat":
		return "wechat"
	case "obsidian":
		return "obsidian"
	default:
		return ""
	}
}
