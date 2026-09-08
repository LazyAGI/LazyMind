package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

type forkAttachment struct {
	Name      string                            `json:"name"`
	Status    string                            `json:"status"`
	Reason    string                            `json:"reason,omitempty"`
	Reference doc.ChatSourceAttachmentReference `json:"reference"`
	Path      string                            `json:"-"`
}
type forkStoredAttachment struct {
	Path      string                            `json:"path"`
	Reference doc.ChatSourceAttachmentReference `json:"reference"`
}

func inspectForkAttachments(ctx context.Context, db *gorm.DB, caller doc.DatasetCatalogCaller, histories []orm.ChatHistory) ([]forkAttachment, error) {
	paths := sidechatSourceFileRefs(histories)
	if len(paths) > 200 {
		return nil, forkFail("FORK_TOO_LARGE")
	}
	storedRefs := map[string]doc.ChatSourceAttachmentReference{}
	conflicts := map[string]bool{}
	for _, h := range histories {
		var ext struct {
			Attachments []forkStoredAttachment `json:"fork_attachments"`
		}
		_ = json.Unmarshal(h.Ext, &ext)
		for _, a := range ext.Attachments {
			if previous, ok := storedRefs[a.Path]; ok && previous != a.Reference {
				conflicts[a.Path] = true
			}
			storedRefs[a.Path] = a.Reference
		}
	}
	out := make([]forkAttachment, 0, len(paths))
	for _, path := range paths {
		item := forkAttachment{Name: filepath.Base(path), Path: path, Status: "available"}
		ref := storedRefs[path]
		ref.Path = path
		refs, err := doc.ValidateChatSourceAttachments(ctx, db, caller, []doc.ChatSourceAttachmentReference{ref})
		if conflicts[path] && err == nil {
			err = doc.ErrChatSourceAttachmentUnavailable
		}
		if err != nil {
			if !errors.Is(err, doc.ErrChatSourceAttachmentUnavailable) && !errors.Is(err, doc.ErrChatSourceAttachmentForbidden) {
				return nil, err
			}
			item.Status = "unavailable"
			item.Reason = "ATTACHMENT_UNAVAILABLE"
		} else {
			item.Reference = refs[0]
			item.Reference.Path = ""
		}
		out = append(out, item)
	}
	return out, nil
}

func forkAttachmentWarnings(items []forkAttachment) []string {
	warnings := []string{}
	for _, item := range items {
		if item.Status != "available" {
			warnings = append(warnings, "ATTACHMENT_UNAVAILABLE")
			break
		}
	}
	return warnings
}

func applyForkAttachments(histories []orm.ChatHistory, attachments []forkAttachment) error {
	byPath := map[string]forkAttachment{}
	for _, attachment := range attachments {
		byPath[attachment.Path] = attachment
	}
	for i := range histories {
		var ext map[string]any
		if err := json.Unmarshal(histories[i].Ext, &ext); err != nil {
			return err
		}
		refs := []forkStoredAttachment{}
		inputs, _ := ext["input"].([]any)
		for _, raw := range inputs {
			input, _ := raw.(map[string]any)
			if input == nil {
				continue
			}
			path, _ := input["uri"].(string)
			if path == "" {
				continue
			}
			item, ok := byPath[path]
			if !ok || item.Status != "available" {
				input["filename"] = filepath.Base(path)
				delete(input, "uri")
				delete(input, "url")
				delete(input, "content")
				delete(input, "input_base64")
				input["fork_unavailable"] = true
			} else {
				refs = append(refs, forkStoredAttachment{Path: path, Reference: item.Reference})
			}
		}
		ext["fork_attachments"] = refs
		histories[i].Ext = marshalChatHistoryExt(ext)
	}
	return nil
}

// Refresh attachment availability for display without rewriting the stored
// transcript or removing ordinary answer text.
func refreshForkAttachmentsForRead(ctx context.Context, db *gorm.DB, caller doc.DatasetCatalogCaller, histories []orm.ChatHistory) ([]orm.ChatHistory, error) {
	out := append([]orm.ChatHistory(nil), histories...)
	for i, h := range out {
		var ext map[string]any
		_ = json.Unmarshal(h.Ext, &ext)
		if ext["fork_read_only"] != true {
			continue
		}
		items := []orm.ChatHistory{h}
		attachments, err := inspectForkAttachments(ctx, db, caller, items)
		if err != nil {
			return nil, err
		}
		if err := applyForkAttachments(items, attachments); err != nil {
			return nil, err
		}
		out[i] = items[0]
	}
	return out, nil
}

// Revalidation happens on each model request, including a fork of a fork.
// A withdrawn attachment's derived payload must not be sent to the model again.
func revalidateForkHistoryAttachments(ctx context.Context, db *gorm.DB, caller doc.DatasetCatalogCaller, histories []orm.ChatHistory) ([]orm.ChatHistory, error) {
	out := append([]orm.ChatHistory(nil), histories...)
	var localSources map[string]bool
	for _, h := range histories {
		var flags struct {
			ReadOnly bool `json:"fork_read_only"`
		}
		if json.Unmarshal(h.Ext, &flags) != nil || !flags.ReadOnly || len(forkConfigFromHistory(h).LocalFSSourceIDs) == 0 {
			continue
		}
		r := &http.Request{Header: http.Header{}}
		r.Header.Set("Authorization", caller.Authorization)
		r.Header.Set("X-Tenant-ID", caller.TenantID)
		r.Header.Set("X-User-Role", caller.UserRole)
		sources, err := loadLocalFSSourcesForChat(ctx, r, caller.UserID)
		if err != nil {
			return nil, err
		}
		localSources = map[string]bool{}
		for _, source := range sources {
			if id, ok := source["source_id"].(string); ok {
				localSources[id] = true
			}
		}
		break
	}
	for i, h := range out {
		var ext map[string]any
		if json.Unmarshal(h.Ext, &ext) != nil || ext["fork_read_only"] != true {
			continue
		}
		var stored struct {
			Attachments []forkStoredAttachment `json:"fork_attachments"`
		}
		if err := json.Unmarshal(h.Ext, &stored); err != nil {
			return nil, err
		}
		items := make([]forkAttachment, 0, len(stored.Attachments))
		unavailable := false
		if inputs, ok := ext["input"].([]any); ok {
			for _, input := range inputs {
				if item, ok := input.(map[string]any); ok && item["fork_unavailable"] == true {
					unavailable = true
				}
			}
		}
		config := forkConfigFromHistory(h)
		for _, datasetID := range stringSliceFromAny(config.Filters["kb_id"]) {
			if err := authorizeKnowledgeBaseID(ctx, db, caller.UserID, datasetID); err != nil {
				if !errors.Is(err, errKnowledgeBaseNotReadable) {
					return nil, err
				}
				unavailable = true
			}
		}
		for _, id := range config.LocalFSSourceIDs {
			if !localSources[id] {
				unavailable = true
			}
		}
		for _, attachment := range stored.Attachments {
			ref := attachment.Reference
			ref.Path = attachment.Path
			_, err := doc.ValidateChatSourceAttachments(ctx, db, caller, []doc.ChatSourceAttachmentReference{ref})
			item := forkAttachment{Path: attachment.Path, Reference: ref, Status: "available"}
			if err != nil {
				if !errors.Is(err, doc.ErrChatSourceAttachmentUnavailable) && !errors.Is(err, doc.ErrChatSourceAttachmentForbidden) {
					return nil, err
				}
				item.Status = "unavailable"
				unavailable = true
			}
			items = append(items, item)
		}
		if err := applyForkAttachments(out[i:i+1], items); err != nil {
			return nil, err
		}
		if unavailable {
			out[i].Content = out[i].RawContent
			out[i].RetrievalResult = nil
			out[i].Result = stripToolTags(out[i].Result)
			var projected map[string]any
			_ = json.Unmarshal(out[i].Ext, &projected)
			projected["fork_restricted_context"] = true
			out[i].Ext = marshalChatHistoryExt(projected)
		}
	}
	return out, nil
}
