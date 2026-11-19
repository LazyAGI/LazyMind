package doc

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

var errEmbeddingStateChanged = errors.New("knowledge index changed; retry after model switching finishes")

func parsingEmbeddingGeneration(ctx context.Context, db *gorm.DB) (int64, error) {
	return documentTaskGeneration(ctx, db, true)
}

func documentTaskGeneration(ctx context.Context, db *gorm.DB, indexing bool) (int64, error) {
	if !db.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		return 0, nil
	}
	var state orm.EmbeddingIndexState
	err := db.WithContext(ctx).Where("scope = ?", "global").Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil || (indexing && (state.Phase == "clearing" || state.Phase == "cleanup_failed" || state.Phase == "cleared" || state.Phase == "detached")) {
		return 0, errEmbeddingStateChanged
	}
	return state.Generation, nil
}

// The worker rejects a late queue submission that carries an old model config.
func loadParsingLLMConfig(ctx context.Context, db *gorm.DB, userID string, levels ...string) (map[string]any, error) {
	// Even non-indexing reparses can remove vectors; stamp their generation.
	indexing := len(levels) == 0 || levels[0] == ProcessingLevelIndexed
	generation, err := documentTaskGeneration(ctx, db, indexing)
	if err != nil {
		return nil, err
	}
	config, loadErr := modelconfig.LoadLLMConfig(ctx, db, userID)
	current, err := documentTaskGeneration(ctx, db, indexing)
	if err != nil || current != generation {
		return nil, errEmbeddingStateChanged
	}
	if config == nil {
		config = make(map[string]any)
	}
	config["_embedding_generation"] = generation
	return config, loadErr
}
