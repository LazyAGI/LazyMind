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
	if !db.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		return 0, nil
	}
	var state orm.EmbeddingIndexState
	err := db.WithContext(ctx).Where("scope = ?", "global").Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil || state.Phase == "clearing" || state.Phase == "cleanup_failed" || state.Phase == "cleared" {
		return 0, errEmbeddingStateChanged
	}
	return state.Generation, nil
}

// The worker rejects a late queue submission that carries an old model config.
func loadParsingLLMConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	generation, err := parsingEmbeddingGeneration(ctx, db)
	if err != nil {
		return nil, err
	}
	config, loadErr := modelconfig.LoadLLMConfig(ctx, db, userID)
	current, err := parsingEmbeddingGeneration(ctx, db)
	if err != nil || current != generation {
		return nil, errEmbeddingStateChanged
	}
	if config == nil {
		config = make(map[string]any)
	}
	config["_embedding_generation"] = generation
	return config, loadErr
}
