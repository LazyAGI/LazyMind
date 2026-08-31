package cloudclient

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

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
