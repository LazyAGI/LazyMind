package cloudusage

import (
	"context"
	"errors"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

type fakeTokens struct {
	token string
	err   error
	ttl   time.Duration
}

func (f *fakeTokens) AccessToken(_ context.Context, ttl time.Duration) (string, error) {
	f.ttl = ttl
	return f.token, f.err
}

type fakePlanClient struct {
	token string
	plan  cloudclient.AccountTokenPlan
	calls int
}

func (f *fakePlanClient) GetAccountTokenPlan(_ context.Context, token string) (cloudclient.AccountTokenPlan, error) {
	f.calls++
	f.token = token
	return f.plan, nil
}

func TestCloudSourceUsesTheServerSideDesktopSession(t *testing.T) {
	tokens := &fakeTokens{token: "fixture-access-token"}
	client := &fakePlanClient{plan: cloudclient.AccountTokenPlan{Status: "inactive"}}

	plan, err := (CloudSource{Tokens: tokens, Client: client}).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "inactive" || client.token != "fixture-access-token" || client.calls != 1 {
		t.Fatalf("plan=%+v token=%q calls=%d", plan, client.token, client.calls)
	}
	if tokens.ttl < 30*time.Second {
		t.Fatalf("minimum token TTL = %s", tokens.ttl)
	}
}

func TestCloudSourceDoesNotCallCloudWithoutASession(t *testing.T) {
	tokens := &fakeTokens{err: errors.New("signed out with secret-canary")}
	client := &fakePlanClient{}

	_, err := (CloudSource{Tokens: tokens, Client: client}).Get(context.Background())
	if !errors.Is(err, ErrCloudSessionUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if client.calls != 0 || client.token != "" {
		t.Fatalf("Cloud called without a session: calls=%d token=%q", client.calls, client.token)
	}
}
