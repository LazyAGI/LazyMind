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
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

var maxShadowBlobBytes int64 = 64 << 20

var ErrShadowTooLarge = errors.New("ARTIFACT_SHADOW_TOO_LARGE")

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
) error {
	if !Enabled() || svc == nil {
		return nil
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
			{ScopeType: ScopeConversation, ScopeID: conversationID, Role: RoleOutput, FollowHead: true},
			{ScopeType: ScopeHistory, ScopeID: historyID, Role: RoleOutput, FollowHead: false},
			{ScopeType: ScopeLegacyRow, ScopeID: row.ID, Role: RoleOutput, FollowHead: false},
		},
	}
	if existing, err := svc.FindByLegacyID(ctx, row.ID); err == nil && existing != nil {
		req.ArtifactID = existing.ArtifactID
	}
	switch row.ContentType {
	case "text", "json":
		req.InlineJSON = row.Value
	case "file":
		blobID, err := storeBlobFromFile(svc.DB, userID, req.MIMEType, row.Value)
		if err != nil {
			log.Warn().Err(err).Str("legacy_artifact_id", row.ID).Msg("[ArtifactV2] dual-write skipped unread file")
			return skipStaleMainChatProjection(ctx, svc, row.ID, err)
		}
		req.BlobID = blobID
	default:
		err := errors.New("unsupported artifact content type")
		log.Warn().Str("legacy_artifact_id", row.ID).Str("content_type", row.ContentType).
			Msg("[ArtifactV2] dual-write skipped unsupported content type")
		return skipStaleMainChatProjection(ctx, svc, row.ID, err)
	}
	if req.IdempotencyKey == "" {
		sum := sha256.Sum256(append(append(append([]byte(row.ID), req.Content...), req.InlineJSON...), []byte(req.BlobID)...))
		req.IdempotencyKey = "legacy/" + row.ID + "/" + historyID + "/" + hex.EncodeToString(sum[:8])
	}
	if _, err := svc.CommitRevision(ctx, req); err != nil {
		log.Warn().Err(err).Str("legacy_artifact_id", row.ID).Msg("[ArtifactV2] dual-write skipped")
		return skipStaleMainChatProjection(ctx, svc, row.ID, err)
	}
	return nil
}

func skipStaleMainChatProjection(ctx context.Context, svc *Service, legacyID string, err error) error {
	if svc != nil {
		_ = svc.DropLegacyBindings(ctx, ScopeLegacyRow, legacyID)
	}
	return err
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

func BindForkConversation(ctx context.Context, svc *Service, ownerUserID, sourceLegacyID, childConversationID, childLegacyID string) error {
	if !Enabled() || svc == nil {
		return nil
	}
	binding, err := svc.FindLatestLegacyBinding(ctx, ScopeLegacyRow, sourceLegacyID)
	if errors.Is(err, ErrNotFound) {
		binding, err = svc.FindLatestLegacyBinding(ctx, ScopeSubAgentLegacyRow, sourceLegacyID)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	revisionID := strings.TrimSpace(binding.RevisionID)
	if revisionID == "" {
		head, err := svc.Head(ctx, binding.ArtifactID, ChannelPublished)
		if err != nil {
			return nil
		}
		revisionID = head.RevisionID
	}
	return BindForkRevision(ctx, svc, ownerUserID, revisionID, childConversationID, childLegacyID, "")
}

// BindForkRevision uses the revision captured by the fork checkpoint. Reading a
// head or a mutable legacy mapping again would change the selected history.
func BindForkRevision(ctx context.Context, svc *Service, ownerUserID, revisionID, childConversationID, childLegacyID, childHistoryID string) error {
	rev, art, err := svc.GetRevision(ctx, ownerUserID, revisionID)
	if err != nil {
		return err
	}
	if strings.EqualFold(rev.ContentType, "file_list") && childHistoryID == "" {
		// Legacy fork expands a file_list into per-file child rows. Reusing the
		// source zip blob would make those children download the archive.
		return nil
	}
	bindings := []BindingSpec{{
		ScopeType: ScopeConversation, ScopeID: childConversationID, Role: RoleOutput, FollowHead: true,
	}}
	if strings.TrimSpace(childLegacyID) != "" {
		bindings = append(bindings, BindingSpec{
			ScopeType: ScopeLegacyRow, ScopeID: childLegacyID, Role: RoleOutput, FollowHead: false,
		})
	}
	if childHistoryID != "" {
		bindings = append(bindings, BindingSpec{ScopeType: ScopeHistory, ScopeID: childHistoryID, Role: RoleOutput, FollowHead: false})
	}
	contentType := rev.ContentType
	if contentType == "file_list" {
		contentType = "file"
	}
	var metadata struct {
		Filename string `json:"filename"`
	}
	_ = json.Unmarshal(rev.Metadata, &metadata)
	_, err = svc.CommitRevision(ctx, CommitRequest{
		TenantID: art.TenantID, OwnerUserID: ownerUserID,
		LogicalKey: ConversationScopedLogicalKey(childConversationID, DisplayLogicalKey(art.LogicalKey)),
		Title:      firstNonEmpty(metadata.Filename, art.Title), Kind: art.Kind, Caption: rev.Caption,
		IdempotencyKey: "fork/" + childConversationID + "/" + childLegacyID + "/" + revisionID,
		InlineJSON:     rev.InlineJSON, BlobID: rev.BlobID, ContentType: contentType,
		ProducerType: ProducerMainChat, Channel: ChannelPublished, Bindings: bindings,
	})
	return err
}

func readMainChatFilePath(raw json.RawMessage) (string, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return "", errors.New("file artifact value must be an object")
	}
	path, _ := value["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", errors.New("file artifact path is missing")
	}
	return path, nil
}

func storeBlobFromFile(db *gorm.DB, tenant, mime string, raw json.RawMessage) (string, error) {
	path, err := readMainChatFilePath(raw)
	if err != nil {
		return "", err
	}
	return ingestFileBlob(db, tenant, mime, path)
}

func ingestFileBlob(db *gorm.DB, tenant, mime, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("file artifact path is not a regular file")
	}
	if info.Size() > maxShadowBlobBytes {
		return "", ErrShadowTooLarge
	}
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()
	ref, err := PutBlob(tenant, mime, io.LimitReader(source, maxShadowBlobBytes+1), "", info.Size())
	if err != nil {
		return "", err
	}
	if ref.Size > maxShadowBlobBytes {
		return "", ErrShadowTooLarge
	}
	return persistBlob(db, ref, info.ModTime().UTC())
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
