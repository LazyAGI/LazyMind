package artifact

import (
	"context"
	"encoding/json"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

// PublishedProjection is a logical file, not a delivery receipt. Only the
// published head selects its bytes; legacy identifiers are compatibility keys.
type PublishedProjection struct {
	LegacyProjection
	ProducerType string
	ProducerID   string
	CreatedAt    time.Time
	LegacyID     string
	HistoryID    string
	SnapshotPath string
}

type projectionRow struct {
	ArtifactID    string
	LogicalKey    string
	Title         string
	RevisionID    string
	RevisionNo    int64
	RevisionCount int
	HeadVersion   int64
	ContentType   string
	Caption       *string
	InlineJSON    json.RawMessage
	Metadata      json.RawMessage
	StorageKey    string
	ProducerType  string
	ProducerID    string
	CreatedAt     time.Time
	LegacyID      string
	HistoryID     string
}

// ConversationPublished uses a fixed number of queries. It loads one payload
// per logical artifact; revision history is counted in SQL, never materialized.
// Bindings include deleted artifacts so callers must not resurrect their legacy
// delivery rows as an unmapped fallback.
func (s *Service) ConversationPublished(ctx context.Context, owner, conversationID string) ([]PublishedProjection, []orm.ArtifactBinding, error) {
	ids := s.DB.WithContext(ctx).Model(&orm.ArtifactBinding{}).
		Select("artifact_id").Where("scope_type = ? AND scope_id = ? AND validity = ?", ScopeConversation, conversationID, ValidityEffective)
	var bindings []orm.ArtifactBinding
	if err := s.DB.WithContext(ctx).Where("artifact_id IN (?)", ids).
		Where("artifact_id IN (?)", s.DB.Model(&orm.ArtifactV2{}).Select("id").Where("owner_user_id = ?", owner)).
		Order("created_at ASC, id ASC").Find(&bindings).Error; err != nil {
		return nil, nil, err
	}
	counts := s.DB.Model(&orm.ArtifactRevision{}).Select("artifact_id, COUNT(*) AS revision_count").
		Where("artifact_id IN (?)", ids).Group("artifact_id")
	var rows []projectionRow
	err := s.DB.WithContext(ctx).Table("artifacts AS a").
		Select(`a.id AS artifact_id, a.logical_key, a.title, a.created_at,
		 r.id AS revision_id, r.revision_no, r.content_type, r.caption, r.inline_json, r.metadata,
		 r.producer_type, r.producer_id, h.version AS head_version, counts.revision_count,
		 COALESCE(b.storage_key, '') AS storage_key`).
		Joins("JOIN artifact_heads h ON h.artifact_id = a.id AND h.channel = ?", ChannelPublished).
		Joins("JOIN artifact_revisions r ON r.id = h.revision_id AND r.artifact_id = a.id").
		Joins("JOIN (?) counts ON counts.artifact_id = a.id", counts).
		Joins("LEFT JOIN artifact_blobs b ON b.id = r.blob_id AND b.tenant_id = a.tenant_id").
		Where("a.id IN (?) AND a.owner_user_id = ? AND a.deleted_at IS NULL", ids, owner).
		Order("a.created_at ASC, a.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	return projectRows(rows), bindings, nil
}

// ConversationDeliveries resolves immutable history receipts, regardless of
// head movements or historical FollowHead flags written by the shadow rollout.
func (s *Service) ConversationDeliveries(ctx context.Context, owner, conversationID string) ([]PublishedProjection, error) {
	ids := s.DB.Model(&orm.ArtifactBinding{}).Select("artifact_id").Where("scope_type = ? AND scope_id = ?", ScopeConversation, conversationID)
	var rows []projectionRow
	err := s.DB.WithContext(ctx).Table("artifact_bindings hb").
		Select(`a.id AS artifact_id, a.logical_key, a.title, r.created_at, r.id AS revision_id,
		 r.revision_no, r.content_type, r.caption, r.inline_json, r.metadata, r.producer_type, r.producer_id,
		 lb.scope_id AS legacy_id, hb.scope_id AS history_id, COALESCE(b.storage_key, '') AS storage_key`).
		Joins("JOIN artifacts a ON a.id = hb.artifact_id").
		Joins("JOIN artifact_revisions r ON r.id = hb.revision_id AND r.artifact_id = a.id").
		Joins("JOIN artifact_bindings lb ON lb.artifact_id = a.id AND lb.revision_id = r.id AND lb.scope_type IN ?", []string{ScopeLegacyRow, ScopeSubAgentLegacyRow}).
		Joins("LEFT JOIN artifact_blobs b ON b.id = r.blob_id AND b.tenant_id = a.tenant_id").
		Where("a.id IN (?) AND a.owner_user_id = ? AND a.deleted_at IS NULL AND hb.scope_type = ? AND hb.validity = ?", ids, owner, ScopeHistory, ValidityEffective).
		Order("r.revision_no DESC, r.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := rows[:0]
	for _, row := range rows {
		key := row.HistoryID + "/" + row.LegacyID
		if !seen[key] {
			unique = append(unique, row)
			seen[key] = true
		}
	}
	return projectRows(unique), nil
}

func projectRows(rows []projectionRow) []PublishedProjection {
	out := make([]PublishedProjection, 0, len(rows))
	for _, row := range rows {
		p := LegacyProjection{V2ArtifactID: row.ArtifactID, RevisionID: row.RevisionID, RevisionNo: row.RevisionNo,
			Count: row.RevisionCount, LogicalKey: DisplayLogicalKey(row.LogicalKey), HeadVersion: row.HeadVersion,
			ContentType: row.ContentType, Filename: row.Title, Caption: row.Caption, InlineJSON: row.InlineJSON}
		var meta struct {
			Filename      string `json:"filename"`
			ChangeSummary string `json:"change_summary"`
		}
		_ = json.Unmarshal(row.Metadata, &meta)
		if meta.Filename != "" {
			p.Filename = meta.Filename
		}
		p.ChangeSummary = meta.ChangeSummary
		if row.StorageKey != "" {
			p.OverlayValue, _ = json.Marshal(map[string]any{"url": doc.StaticFileURLFromAnyStoragePath(row.StorageKey), "filename": p.Filename})
			// A file_list snapshot is an immutable archive, not mutable workspace members.
			if p.ContentType == "file_list" {
				p.ContentType = "file"
			}
		}
		out = append(out, PublishedProjection{LegacyProjection: p, ProducerType: row.ProducerType, ProducerID: row.ProducerID, CreatedAt: row.CreatedAt, LegacyID: row.LegacyID, HistoryID: row.HistoryID, SnapshotPath: row.StorageKey})
	}
	return out
}

// LegacyReceipts preserves pre-upgrade forks, which have pinned legacy
// bindings but no history binding. Callers supply history metadata from the
// legacy receipt; content always comes from its immutable revision.
func (s *Service) LegacyReceipts(ctx context.Context, owner string, legacyIDs []string) ([]PublishedProjection, error) {
	if len(legacyIDs) == 0 {
		return nil, nil
	}
	var rows []projectionRow
	err := s.DB.WithContext(ctx).Table("artifact_bindings lb").
		Select(`a.id AS artifact_id, a.logical_key, a.title, r.created_at, r.id AS revision_id,
		 r.revision_no, r.content_type, r.caption, r.inline_json, r.metadata, r.producer_type, r.producer_id,
		 lb.scope_id AS legacy_id, COALESCE(b.storage_key, '') AS storage_key`).
		Joins("JOIN artifacts a ON a.id = lb.artifact_id").
		Joins("JOIN artifact_revisions r ON r.id = lb.revision_id AND r.artifact_id = a.id").
		Joins("LEFT JOIN artifact_blobs b ON b.id = r.blob_id AND b.tenant_id = a.tenant_id").
		Where("a.owner_user_id = ? AND a.deleted_at IS NULL AND lb.scope_type = ? AND lb.scope_id IN ?", owner, ScopeLegacyRow, legacyIDs).
		Order("r.revision_no DESC, r.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := rows[:0]
	for _, row := range rows {
		if !seen[row.LegacyID] {
			unique = append(unique, row)
			seen[row.LegacyID] = true
		}
	}
	return projectRows(unique), nil
}
