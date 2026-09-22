package knowledgeplaza

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lazymind/core/cloudclient"
)

var ErrCloudSessionUnavailable = errors.New("LazyMind Cloud session is unavailable")

type AccessTokenSource interface {
	AccessToken(context.Context, time.Duration) (string, error)
}

type KnowledgeClient interface {
	ListKnowledge(context.Context, string, cloudclient.KnowledgeQuery) (cloudclient.KnowledgePage, error)
}

type CloudSource struct {
	Tokens AccessTokenSource
	Client KnowledgeClient
}

func (s CloudSource) List(ctx context.Context, query cloudclient.KnowledgeQuery) (cloudclient.KnowledgePage, error) {
	if s.Tokens == nil || s.Client == nil {
		return cloudclient.KnowledgePage{}, ErrCloudSessionUnavailable
	}
	token, err := s.Tokens.AccessToken(ctx, 30*time.Second)
	if err != nil {
		return cloudclient.KnowledgePage{}, fmt.Errorf("%w: %v", ErrCloudSessionUnavailable, err)
	}
	return s.Client.ListKnowledge(ctx, token, query)
}
