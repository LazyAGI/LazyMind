package core

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/chat"
	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
)

type DatasetGetter interface {
	GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error)
}

type KnowledgeAccessChecker struct {
	datasets DatasetGetter
}

func NewKnowledgeAccessChecker(datasets DatasetGetter) (*KnowledgeAccessChecker, error) {
	if datasets == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.access.new", "dataset service is required", false, nil)
	}
	return &KnowledgeAccessChecker{datasets: datasets}, nil
}

func (c *KnowledgeAccessChecker) EnsureKnowledgeReadable(ctx context.Context, userID string, knowledgeID string) error {
	userID = strings.TrimSpace(userID)
	knowledgeID = strings.TrimSpace(knowledgeID)
	if c == nil || c.datasets == nil {
		return &chat.KnowledgeChatError{Code: chat.KnowledgeChatInternal, Message: "knowledge access checker is not configured"}
	}
	if userID == "" || knowledgeID == "" {
		return &chat.KnowledgeChatError{Code: chat.KnowledgeChatInvalidArgument, Message: "user_id and knowledge_id are required"}
	}
	_, err := c.datasets.GetDataset(ctx, doc.DatasetGetRequest{
		UserID:    userID,
		DatasetID: knowledgeID,
		Caller:    doc.DatasetCatalogCaller{UserID: userID},
	})
	if err != nil {
		return mapDatasetAccessError(err)
	}
	return nil
}

type KnowledgeChatRunner interface {
	RunKnowledgeChat(ctx context.Context, input chat.KnowledgeChatRequest) (chat.KnowledgeChatResult, error)
}

type KnowledgeSearchAdapter struct {
	runner KnowledgeChatRunner
}

func NewKnowledgeSearchAdapter(runner KnowledgeChatRunner) (*KnowledgeSearchAdapter, error) {
	if runner == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.adapter.new", "knowledge chat runner is required", false, nil)
	}
	return &KnowledgeSearchAdapter{runner: runner}, nil
}

func NewKnowledgeSearchAdapterForDB(db *gorm.DB, chatBaseURL string) (*KnowledgeSearchAdapter, error) {
	if db == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.adapter.new", "gorm db is required", false, nil)
	}
	if strings.TrimSpace(chatBaseURL) == "" {
		return nil, contract.NewError(contract.Unsupported, "knowledge.search.adapter.new", "chat upstream endpoint is required", false, nil)
	}
	datasets, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return nil, mapDatasetServiceError("knowledge.search.adapter.new", err)
	}
	checker, err := NewKnowledgeAccessChecker(datasets)
	if err != nil {
		return nil, err
	}
	runner := chat.NewKnowledgeChatRunner(chat.KnowledgeChatRunnerDeps{
		DB:            db,
		BaseURL:       chatBaseURL,
		AccessChecker: checker,
	})
	return NewKnowledgeSearchAdapter(runner)
}

func (a *KnowledgeSearchAdapter) Search(ctx context.Context, callCtx contract.CallContext, input compatknowledge.SearchInput) (compatknowledge.SearchResult, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatknowledge.SearchResult{}, contract.InvalidArgumentError("knowledge.search", "user_id is required")
	}
	req := chat.KnowledgeChatRequest{
		UserID:         userID,
		Query:          strings.TrimSpace(input.Query),
		KnowledgeIDs:   append([]string(nil), input.KnowledgeIDs...),
		ConversationID: strings.TrimSpace(input.ConversationID),
		UseMemory:      false,
		EnablePlugin:   false,
	}
	resp, err := a.runner.RunKnowledgeChat(ctx, req)
	if err != nil {
		return compatknowledge.SearchResult{}, mapKnowledgeChatError("knowledge.search", err)
	}
	return mapKnowledgeSearchResult(resp), nil
}

func mapKnowledgeSearchResult(resp chat.KnowledgeChatResult) compatknowledge.SearchResult {
	return compatknowledge.SearchResult{
		Answer:         resp.Answer,
		Sources:        mapKnowledgeSearchSources(resp.Sources),
		ConversationID: resp.ConversationID,
		MessageID:      resp.MessageID,
	}
}

func mapKnowledgeSearchSources(items []chat.KnowledgeChatSource) []compatknowledge.SearchSource {
	out := make([]compatknowledge.SearchSource, 0, len(items))
	for _, item := range items {
		out = append(out, compatknowledge.SearchSource{
			KnowledgeID: item.KnowledgeID,
			DocumentID:  item.DocumentID,
			ChunkID:     item.ChunkID,
			Title:       item.Title,
			Text:        item.Text,
			Number:      item.Number,
		})
	}
	return out
}

func mapDatasetAccessError(err error) error {
	if err == nil {
		return nil
	}
	var chatErr *chat.KnowledgeChatError
	if errors.As(err, &chatErr) {
		return err
	}
	var svcErr *doc.DatasetServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case doc.DatasetServiceInvalidArgument:
			return &chat.KnowledgeChatError{Code: chat.KnowledgeChatInvalidArgument, Message: svcErr.Message, Cause: err}
		case doc.DatasetServiceNotFound, doc.DatasetServiceForbidden:
			return &chat.KnowledgeChatError{Code: chat.KnowledgeChatNotFound, Message: "knowledge not found", Cause: err}
		case doc.DatasetServiceUnavailable:
			return &chat.KnowledgeChatError{Code: chat.KnowledgeChatBackendUnavailable, Message: "knowledge access unavailable", Cause: err}
		default:
			return &chat.KnowledgeChatError{Code: chat.KnowledgeChatInternal, Message: "knowledge access failed", Cause: err}
		}
	}
	return &chat.KnowledgeChatError{Code: chat.KnowledgeChatInternal, Message: "knowledge access failed", Cause: err}
}

func mapKnowledgeChatError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var compatErr *contract.Error
	if errors.As(err, &compatErr) {
		return err
	}
	var chatErr *chat.KnowledgeChatError
	if errors.As(err, &chatErr) {
		switch chatErr.Code {
		case chat.KnowledgeChatInvalidArgument:
			return contract.NewError(contract.InvalidArgument, operation, chatErr.Message, false, err)
		case chat.KnowledgeChatNotFound, chat.KnowledgeChatForbidden:
			return contract.NewError(contract.NotFound, operation, "knowledge search not found", false, err)
		case chat.KnowledgeChatBackendUnavailable:
			return contract.NewError(contract.BackendUnavailable, operation, "backend unavailable", true, err)
		default:
			return contract.NewError(contract.Internal, operation, "internal error", false, err)
		}
	}
	return contract.NewError(contract.Internal, operation, "internal error", false, err)
}
