package cloudclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

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
