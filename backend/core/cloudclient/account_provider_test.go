package cloudclient

import (
	"context"
	"net/http"
	"testing"
)

func TestAccountAndProviderBootstrapUsePublishedPaths(t *testing.T) {
	paths := []string{}
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/v1/account/me":
			return jsonResponse(http.StatusOK, `{"id":"account-1","username":"fixture","email_masked":"f***@example.com","roles":["user"],"effective_permission_keys":[],"status":"active","rbac_version":1,"policy_revision":1}`, nil), nil
		case "/v1/provider-bootstrap":
			return jsonResponse(http.StatusOK, `{"provider_key":"lazymind-cloud","display_name":"LazyMind Cloud","available":true,"model_key":"lazymind-text-default","config_version":1,"refreshed_at":"2026-08-20T10:00:00+08:00","has_token_plan":true,"cloud_chat_available":true,"reason_code":null,"models":[{"model_key":"lazymind-text-default","display_name":"LazyMind Text","capabilities":["chat","stream","tool_calls"],"status":"available"}]}`, nil), nil
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
			return nil, nil
		}
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	account, err := client.GetCurrentAccount(context.Background(), "fixture-access")
	if err != nil || account.ID != "account-1" {
		t.Fatalf("account=%+v err=%v", account, err)
	}
	bootstrap, err := client.GetProviderBootstrap(context.Background(), "fixture-access")
	if err != nil || bootstrap.ModelKey != "lazymind-text-default" {
		t.Fatalf("bootstrap=%+v err=%v", bootstrap, err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
}
