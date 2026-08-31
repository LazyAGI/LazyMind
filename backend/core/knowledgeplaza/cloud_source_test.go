package knowledgeplaza

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

type fakeCloudClient struct {
	token string
	query cloudclient.KnowledgeQuery
}

func (f *fakeCloudClient) ListKnowledge(_ context.Context, token string, query cloudclient.KnowledgeQuery) (cloudclient.KnowledgePage, error) {
	f.token = token
	f.query = query
	return cloudclient.KnowledgePage{}, nil
}

func TestCloudSourceObtainsShortLivedTokenServerSide(t *testing.T) {
	tokens := &fakeTokens{token: "fixture-access"}
	client := &fakeCloudClient{}
	source := CloudSource{Tokens: tokens, Client: client}
	query := cloudclient.KnowledgeQuery{Keyword: "law"}

	if _, err := source.List(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	if client.token != "fixture-access" || client.query.Keyword != "law" {
		t.Fatalf("Cloud request did not receive server-side credentials: token=%q query=%+v", client.token, client.query)
	}
	if tokens.ttl <= 0 {
		t.Fatalf("minimum token TTL = %s", tokens.ttl)
	}
}

func TestCloudSourceStopsWhenSessionIsUnavailable(t *testing.T) {
	tokens := &fakeTokens{err: errors.New("signed out")}
	client := &fakeCloudClient{}
	_, err := (CloudSource{Tokens: tokens, Client: client}).List(context.Background(), cloudclient.KnowledgeQuery{})
	if err == nil {
		t.Fatal("expected session error")
	}
	if client.token != "" {
		t.Fatal("Cloud must not be called without a session")
	}
}
