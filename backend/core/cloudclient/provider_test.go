package cloudclient

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
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

func TestProviderBootstrapRequestsAndDecodesCatalogV2(t *testing.T) {
	var requestPath, requestVersion string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestPath = request.URL.Path
		requestVersion = request.URL.Query().Get("catalog_version")
		return jsonResponse(http.StatusOK, `{
			"provider_key":"lazymind-cloud",
			"display_name":"LazyMind Cloud",
			"available":true,
			"model_key":"lazymind-text-default",
			"config_version":7,
			"catalog_revision":"catalog-user-7",
			"refreshed_at":"2026-09-06T08:00:00Z",
			"has_token_plan":true,
			"cloud_chat_available":true,
			"reason_code":null,
			"models":[{
				"model_key":"lazymind-text-default",
				"display_name":"LazyMind Text",
				"model_type":"llm",
				"capabilities":["chat","stream","tool_calls"],
				"status":"available",
				"lifecycle":"active",
				"default_for_type":true
			}]
		}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	bootstrap, err := client.GetProviderBootstrap(context.Background(), "fixture-access")
	if err != nil {
		t.Fatalf("decode Provider Bootstrap V2: %v", err)
	}
	if requestPath != "/v1/provider-bootstrap" || requestVersion != "2" {
		t.Fatalf("Provider Bootstrap request=%s?catalog_version=%q", requestPath, requestVersion)
	}
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["catalog_revision"] != "catalog-user-7" {
		t.Fatalf("catalog_revision=%#v", payload["catalog_revision"])
	}
	models, _ := payload["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models=%#v", payload["models"])
	}
	model, _ := models[0].(map[string]any)
	if model["model_type"] != "llm" || model["default_for_type"] != true || model["lifecycle"] != "active" {
		t.Fatalf("Cloud model selection metadata=%#v", model)
	}
}

func TestProviderBootstrapV2StillRejectsUpstreamConfiguration(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"provider_key":"lazymind-cloud",
			"display_name":"LazyMind Cloud",
			"available":true,
			"model_key":"lazymind-text-default",
			"catalog_revision":"catalog-user-7",
			"has_token_plan":true,
			"cloud_chat_available":true,
			"reason_code":null,
			"models":[{
				"model_key":"lazymind-text-default",
				"display_name":"LazyMind Text",
				"model_type":"llm",
				"capabilities":["chat"],
				"status":"available",
				"lifecycle":"active",
				"default_for_type":true,
				"upstream_model":"must-not-cross-the-contract"
			}]
		}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetProviderBootstrap(context.Background(), "fixture-access"); err == nil {
		t.Fatal("Provider Bootstrap accepted Cloud-internal upstream configuration")
	}
}

func TestProviderBootstrapAcceptsNoPlanStateWithEmptyModels(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"provider_key":"lazymind-cloud",
			"display_name":"LazyMind Cloud",
			"available":false,
			"model_key":"lazymind-text-default",
			"refreshed_at":"2026-08-21T10:00:00+08:00",
			"has_token_plan":false,
			"cloud_chat_available":false,
			"reason_code":"token_plan_required",
			"models":[]
		}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := client.GetProviderBootstrap(context.Background(), "fixture-access")
	if err != nil {
		t.Errorf("no-Plan Bootstrap must be a valid state, got %v", err)
	}
	encoded := bootstrapJSONFields(bootstrap)
	if _, ok := encoded["has_token_plan"]; !ok {
		t.Error("ModelProviderBootstrap omitted has_token_plan")
	}
	if _, ok := encoded["cloud_chat_available"]; !ok {
		t.Error("ModelProviderBootstrap omitted cloud_chat_available")
	}
	if _, ok := encoded["reason_code"]; !ok {
		t.Error("ModelProviderBootstrap omitted reason_code")
	}
	for _, forbidden := range []string{"periodic_quota", "remaining", "next_refresh_at", "usage", "plan_id", "version"} {
		if _, found := encoded[forbidden]; found {
			t.Errorf("Desktop Bootstrap DTO leaked Cloud-only Plan detail %q", forbidden)
		}
	}
}

func bootstrapJSONFields(value ModelProviderBootstrap) map[string]struct{} {
	fields := map[string]struct{}{}
	typeOf := reflect.TypeOf(value)
	for index := 0; index < typeOf.NumField(); index++ {
		tag := typeOf.Field(index).Tag.Get("json")
		for end, char := range tag {
			if char == ',' {
				tag = tag[:end]
				break
			}
		}
		if tag != "" && tag != "-" {
			fields[tag] = struct{}{}
		}
	}
	return fields
}
