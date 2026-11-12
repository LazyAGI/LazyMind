package doc

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"time"
)

// Defaults describe future work, not a ceiling on already available artifacts.
// Read retained evidence in one batch for catalog pages, including stored libraries.
func addRetainedCapabilities(ctx context.Context, db *gorm.DB, datasets []Dataset) {
	if len(datasets) == 0 || !db.Migrator().HasTable(&orm.DocumentProcessingState{}) {
		return
	}
	ids := make([]string, 0, len(datasets))
	for _, ds := range datasets {
		ids = append(ids, ds.DatasetID)
	}
	var rows []struct {
		DatasetID        string
		ChunkStatus      string
		IndexStatus      string
		EmbeddingVersion string
	}
	err := db.WithContext(ctx).Table("document_processing_states s").
		Select("s.dataset_id,s.chunk_status,s.index_status,s.embedding_version").
		Joins("JOIN documents d ON d.dataset_id=s.dataset_id AND d.id=s.document_id AND d.deleted_at IS NULL").
		Where("s.dataset_id IN ? AND (d.document_type IS NULL OR d.document_type <> ?)", ids, "FOLDER").Scan(&rows).Error
	if err != nil {
		return
	}
	evidence := map[string]DatasetCapabilities{}
	for _, row := range rows {
		caps := evidence[row.DatasetID]
		caps.Search = caps.Search || row.ChunkStatus == "succeeded"
		caps.Retrieve = caps.Retrieve || (row.IndexStatus == "succeeded" && row.EmbeddingVersion != embeddingVersionRebuildRequired)
		caps.Search = caps.Search || caps.Retrieve
		evidence[row.DatasetID] = caps
	}
	for i := range datasets {
		caps := evidence[datasets[i].DatasetID]
		datasets[i].Capabilities.Search = datasets[i].Capabilities.Search || caps.Search
		datasets[i].Capabilities.Retrieve = datasets[i].Capabilities.Retrieve || caps.Retrieve
	}
}

func capabilitiesWithRetainedArtifacts(ctx context.Context, db *gorm.DB, ds orm.Dataset) DatasetCapabilities {
	rows := []Dataset{{DatasetID: ds.ID, Capabilities: capabilitiesForProcessingLevel(ds.ProcessingLevel)}}
	addRetainedCapabilities(ctx, db, rows)
	return rows[0].Capabilities
}

// Source rebuilds may change text even when the library default is unchanged.
// Retain artifacts, but require an explicit fresh index before retrieving them.
const embeddingVersionRebuildRequired = "rebuild:manual"

func markSourceRebuildPending(ctx context.Context, db *gorm.DB, datasetID string, tasks []orm.Task) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, task := range tasks {
			state := orm.DocumentProcessingState{DatasetID: datasetID, DocumentID: task.DocID,
				IndexStatus: "pending", EmbeddingVersion: embeddingVersionRebuildRequired,
				IndexArtifactRef: "core-task:" + task.ID, UpdatedAt: time.Now().UTC()}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "dataset_id"}, {Name: "document_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"index_status", "embedding_version", "index_artifact_ref", "index_error_code", "index_error_message", "updated_at"}),
			}).Create(&state).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func textIndexReparseMode(state orm.DocumentProcessingState) string {
	if state.EmbeddingVersion == embeddingVersionRebuildRequired {
		return "rebuild"
	}
	return "slice_and_embed"
}

func textIndexReparseGroups(state orm.DocumentProcessingState) []string {
	if state.EmbeddingVersion == embeddingVersionRebuildRequired {
		return nil
	}
	return []string{"block", "line", "code", "doc-summary"}
}
