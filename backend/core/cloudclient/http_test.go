package cloudclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCloudBusinessRequestsRejectMissingCredentialsBeforeNetwork(t *testing.T) {
	calls := 0
	client, err := New("https://cloud.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("Cloud is offline")
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, test := range []struct {
		name string
		call func() error
	}{
		{"account", func() error { _, err := client.GetCurrentAccount(ctx, ""); return err }},
		{"models", func() error { _, err := client.GetProviderBootstrap(ctx, ""); return err }},
		{"usage", func() error { _, err := client.GetAccountTokenPlan(ctx, ""); return err }},
		{"resources", func() error {
			_, err := client.ListResources(ctx, "", ResourceQuery{ResourceType: "skill", PageSize: 20})
			return err
		}},
		{"resource metadata", func() error { _, _, err := client.GetResource(ctx, "", "resource-1"); return err }},
		{"resource tree", func() error { _, _, err := client.GetResourceTree(ctx, "", "resource-1"); return err }},
		{"connections", func() error { _, err := client.ListProviderConnections(ctx, ""); return err }},
		{"authorization", func() error {
			_, err := client.CreateProviderConnectionSession(ctx, "", "feishu", "client-1")
			return err
		}},
		{"backup", func() error { _, _, err := client.EnableCredentialVault(ctx, ""); return err }},
		{"restore discovery", func() error { _, err := client.ListCredentialVaultRecords(ctx, "", "", 20); return err }},
		{"knowledge", func() error { _, err := client.ListKnowledgeMarket(ctx, "", nil); return err }},
		{"legacy knowledge", func() error { _, err := client.ListKnowledge(ctx, "", KnowledgeQuery{}); return err }},
		{"refresh", func() error { _, err := client.RefreshSession(ctx, ""); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("missing credentials were accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("unsigned Cloud client made %d network requests", calls)
	}
}

type trackedCloudBody struct {
	io.Reader
	closed bool
}

func (body *trackedCloudBody) Close() error { body.closed = true; return nil }

func TestSharedCloudRequestPreservesErrorsAndClosesResponses(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"success", 200, `{"id":"account-1","username":"test","roles":["user"],"status":"active","rbac_version":1,"policy_revision":1}`, false},
		{"unknown field", 200, `{"unexpected":true}`, true},
		{"unauthorized", 401, `{"code":3000001,"message":"not signed in","request_id":"test-request"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedCloudBody{Reader: strings.NewReader(test.body)}
			client, err := New("https://cloud.example", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("shared request changed authentication")
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: body}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetCurrentAccount(context.Background(), "fixture-token")
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !body.closed {
				t.Fatal("response body was not closed")
			}
			if test.status == 401 {
				var cloudErr *CloudError
				if !errors.As(err, &cloudErr) || cloudErr.HTTPStatus != 401 || cloudErr.Code != 3000001 {
					t.Fatalf("Cloud error identity changed: %v", err)
				}
			}
		})
	}
}
