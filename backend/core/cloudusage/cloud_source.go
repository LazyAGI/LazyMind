package cloudusage

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

type TokenPlanClient interface {
	GetAccountTokenPlan(context.Context, string) (cloudclient.AccountTokenPlan, error)
}

type CloudSource struct {
	Tokens AccessTokenSource
	Client TokenPlanClient
}

func (s CloudSource) Get(ctx context.Context) (cloudclient.AccountTokenPlan, error) {
	if s.Tokens == nil || s.Client == nil {
		return cloudclient.AccountTokenPlan{}, ErrCloudSessionUnavailable
	}
	token, err := s.Tokens.AccessToken(ctx, 30*time.Second)
	if err != nil {
		return cloudclient.AccountTokenPlan{}, fmt.Errorf("%w: %v", ErrCloudSessionUnavailable, err)
	}
	return s.Client.GetAccountTokenPlan(ctx, token)
}
