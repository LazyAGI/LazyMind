package coreadapter

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
)

const (
	textEmbeddingRole       = "embed_main"
	embeddingNotReadyReason = "EMBEDDING_NOT_READY"
)

var retrievalModelRoles = [...]string{"llm", textEmbeddingRole, "reranker", "embed_image"}

type RetrievalModelConfigLoader interface {
	LoadRetrievalModelConfig(context.Context, string) (map[string]any, error)
}

type DBBackedRetrievalModelConfigLoader struct{ db *gorm.DB }

func NewDBBackedRetrievalModelConfigLoader(db *gorm.DB) (*DBBackedRetrievalModelConfigLoader, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.search.models.new", "gorm db is required", false, nil)
	}
	return &DBBackedRetrievalModelConfigLoader{db: db}, nil
}

func (l *DBBackedRetrievalModelConfigLoader) LoadRetrievalModelConfig(ctx context.Context, userID string) (map[string]any, error) {
	const operation = "knowledge.search.models"
	userID = strings.TrimSpace(userID)
	ready, err := modelprovider.IsModelReady(ctx, l.db, userID, textEmbeddingRole)
	if err != nil {
		return nil, mapEmbeddingReadinessCheckError(operation, err)
	}
	if !ready {
		return nil, embeddingNotReadyError(operation, "model_unavailable")
	}

	config, err := modelconfig.LoadLLMConfig(ctx, l.db, userID)
	if err != nil {
		return nil, capability.NewError(capability.Unavailable, operation, "load retrieval model configuration failed", true, err)
	}
	filtered := filterRetrievalModelConfig(config)
	dynamic, err := modelprovider.FetchRoleIsDynamic(ctx, textEmbeddingRole)
	if err != nil {
		return nil, mapEmbeddingReadinessCheckError(operation, err)
	}
	if dynamic && !modelconfig.HasRuntimeSource(filtered[textEmbeddingRole]) {
		return nil, embeddingNotReadyError(operation, "runtime_config_missing")
	}
	return filtered, nil
}

func mapEmbeddingReadinessCheckError(operation string, err error) *capability.Error {
	var upstreamErr *common.HTTPError
	if errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusNotFound {
		return embeddingNotReadyError(operation, "runtime_role_missing")
	}
	return capability.NewError(capability.Unavailable, operation, "check text embedding readiness failed", true, nil)
}

func embeddingNotReadyError(operation, readinessReason string) *capability.Error {
	return &capability.Error{
		Code:      capability.Unavailable,
		Operation: operation,
		Message:   "configure a text embedding model in model defaults to search knowledge",
		Reason:    embeddingNotReadyReason,
		Details: map[string]any{
			"required_model_role": textEmbeddingRole,
			"readiness_reason":    readinessReason,
			"recovery":            "open_model_defaults",
		},
		Action: &capability.ErrorAction{Type: "configure_model", URL: "/settings?section=model_defaults&target=embed_main"},
	}
}

func filterRetrievalModelConfig(config map[string]any) map[string]any {
	filtered := make(map[string]any, len(retrievalModelRoles))
	for _, role := range retrievalModelRoles {
		if roleConfig, ok := config[role]; ok {
			filtered[role] = roleConfig
		}
	}
	return filtered
}
