package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog/log"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

type MainChatWrite struct {
	LogicalKey     string
	IdempotencyKey string
	ChangeSummary  string
}

func DualWriteMainChat(
	ctx context.Context,
	svc *Service,
	conversationID, historyID, userID string,
	meta MainChatWrite,
	row orm.ConversationArtifact,
) {
	if !Enabled() || svc == nil {
		return
	}
	logicalKey := strings.TrimSpace(meta.LogicalKey)
	if logicalKey == "" {
		logicalKey = row.ID
	}
	logicalKey = ConversationScopedLogicalKey(conversationID, logicalKey)
	req := CommitRequest{
		TenantID:        userID,
		OwnerUserID:     userID,
		LogicalKey:      logicalKey,
		Title:           row.Filename,
		Kind:            KindFile,
		IdempotencyKey:  strings.TrimSpace(meta.IdempotencyKey),
		Caption:         row.Caption,
		ChangeSummary:   strings.TrimSpace(meta.ChangeSummary),
		ProducerType:    ProducerMainChat,
		ProducerEventID: row.ID,
		Channel:         ChannelPublished,
		ContentType:     row.ContentType,
		MIMEType:        mimeForLegacy(row.ContentType),
		Bindings: []BindingSpec{
			{ScopeType: ScopeConversation, ScopeID: conversationID, Role: RoleOutput},
			{ScopeType: ScopeHistory, ScopeID: historyID, Role: RoleOutput},
			{ScopeType: ScopeLegacyRow, ScopeID: row.ID, Role: RoleOutput},
		},
	}
	if existing, err := svc.FindByLegacyID(ctx, row.ID); err == nil && existing != nil {
		req.ArtifactID = existing.ArtifactID
		if head, headErr := svc.Head(ctx, existing.ArtifactID, ChannelPublished); headErr == nil {
			req.BaseRevisionID = head.RevisionID
			req.ExpectedHeadVer = head.Version
		}
	}
	switch row.ContentType {
	case "text", "json":
		req.InlineJSON = row.Value
	case "file":
		var value map[string]any
		if json.Unmarshal(row.Value, &value) == nil {
			if path, _ := value["path"].(string); path != "" {
				if data, err := os.ReadFile(path); err == nil {
					req.Content = data
				}
			}
		}
	}
	if req.IdempotencyKey == "" {
		sum := sha256.Sum256(append(append([]byte(row.ID), req.Content...), req.InlineJSON...))
		req.IdempotencyKey = "legacy/" + row.ID + "/" + hex.EncodeToString(sum[:8])
	}
	if _, err := svc.CommitRevision(ctx, req); err != nil {
		log.Warn().Err(err).Str("legacy_artifact_id", row.ID).Msg("[ArtifactV2] dual-write skipped")
	}
}

func mimeForLegacy(contentType string) string {
	switch contentType {
	case "text":
		return "text/plain"
	case "json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func SignRevisionURL(ctx context.Context, svc *Service, ownerUserID, revisionID string) (string, *orm.ArtifactRevision, error) {
	rev, art, err := svc.GetRevision(ctx, ownerUserID, revisionID)
	if err != nil {
		return "", nil, err
	}
	if rev.BlobID == "" {
		return "", rev, nil
	}
	var blob orm.ArtifactBlob
	if err := svc.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", rev.BlobID, art.TenantID).Take(&blob).Error; err != nil {
		return "", rev, ErrNotFound
	}
	url := doc.StaticFileURLFromAnyStoragePath(blob.StorageKey)
	if url == "" {
		return "", rev, ErrNotFound
	}
	return url, rev, nil
}

func StreamRevision(ctx context.Context, svc *Service, ownerUserID, revisionID string, dest io.Writer) error {
	rev, art, err := svc.GetRevision(ctx, ownerUserID, revisionID)
	if err != nil {
		return err
	}
	if len(rev.InlineJSON) > 0 && rev.BlobID == "" {
		_, err = dest.Write(rev.InlineJSON)
		return err
	}
	var blob orm.ArtifactBlob
	if err := svc.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", rev.BlobID, art.TenantID).Take(&blob).Error; err != nil {
		return ErrNotFound
	}
	return RangeRead(BlobRef{TenantID: blob.TenantID, SHA256: blob.SHA256, StorageKey: blob.StorageKey}, 0, 0, dest)
}

func BindForkConversation(ctx context.Context, svc *Service, ownerUserID, sourceLegacyID, childConversationID, childLegacyID string) error {
	if !Enabled() || svc == nil {
		return nil
	}
	binding, err := svc.FindByLegacyID(ctx, sourceLegacyID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	head, err := svc.Head(ctx, binding.ArtifactID, ChannelPublished)
	if err != nil {
		return nil
	}
	if err := svc.BindRevision(ctx, ownerUserID, BindingSpec{
		ScopeType:  ScopeConversation,
		ScopeID:    childConversationID,
		Role:       RoleOutput,
		RevisionID: head.RevisionID,
	}, binding.ArtifactID); err != nil {
		return err
	}
	if strings.TrimSpace(childLegacyID) == "" {
		return nil
	}
	return svc.BindRevision(ctx, ownerUserID, BindingSpec{
		ScopeType:  ScopeLegacyRow,
		ScopeID:    childLegacyID,
		Role:       RoleOutput,
		RevisionID: head.RevisionID,
	}, binding.ArtifactID)
}

func ConversationScopedLogicalKey(conversationID, key string) string {
	return "conv:" + strings.TrimSpace(conversationID) + ":" + strings.TrimSpace(key)
}

func DisplayLogicalKey(stored string) string {
	const prefix = "conv:"
	if !strings.HasPrefix(stored, prefix) {
		return stored
	}
	rest := stored[len(prefix):]
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		return rest[i+1:]
	}
	return stored
}
