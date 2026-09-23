package cloudclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestGetAccountTokenPlanReadsCurrentAccountUsageWithoutBrowserCredentials(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/account/token-plan" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer fixture-access-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := request.Header.Get("Cookie"); got != "" {
			t.Fatalf("Desktop account request must not carry Browser cookies: %q", got)
		}
		return jsonResponse(http.StatusOK, `{
			"status":"active",
			"plan_id":"00000000-0000-7000-8000-000000000701",
			"display_name":"Free Plan",
			"version":3,
			"refresh_cycle":"monthly",
			"next_refresh_at":"2026-10-01T00:00:00Z",
			"model_quotas":[{
				"public_model_key":"lazymind-text-default",
				"capability":"llm",
				"meter_unit":"token",
				"periodic_quota":10000
			}],
			"usage":[{
				"public_model_key":"lazymind-text-default",
				"meter_unit":"token",
				"periodic_quota":10000,
				"used_amount":2500,
				"remaining_amount":7500,
				"missing_usage_count":2
			}]
		}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := client.GetAccountTokenPlan(context.Background(), "fixture-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "active" || plan.DisplayName != "Free Plan" || plan.Version != 3 || plan.RefreshCycle != "monthly" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.NextRefreshAt != "2026-10-01T00:00:00Z" || len(plan.ModelQuotas) != 1 || len(plan.Usage) != 1 {
		t.Fatalf("plan detail = %+v", plan)
	}
	quota := plan.ModelQuotas[0]
	if quota.PublicModelKey != "lazymind-text-default" || quota.Capability != "llm" || quota.MeterUnit != "token" || quota.PeriodicQuota != 10000 {
		t.Fatalf("quota = %+v", quota)
	}
	usage := plan.Usage[0]
	if usage.UsedAmount != 2500 || usage.RemainingAmount != 7500 || usage.MissingUsageCount != 2 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestGetAccountTokenPlanAcceptsInactiveAsARealAccountState(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"status":"inactive"}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := client.GetAccountTokenPlan(context.Background(), "fixture-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "inactive" || len(plan.ModelQuotas) != 0 || len(plan.Usage) != 0 {
		t.Fatalf("inactive plan = %+v", plan)
	}
}

func TestGetAccountTokenPlanRejectsInvalidOrUnboundedCloudPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"status":"inactive","access_token":"secret-canary"}`},
		{name: "unknown status", body: `{"status":"trial"}`},
		{name: "active without details", body: `{"status":"active"}`},
		{name: "negative remaining", body: `{
			"status":"active","plan_id":"00000000-0000-7000-8000-000000000701","display_name":"Free Plan",
			"version":1,"refresh_cycle":"monthly","next_refresh_at":"2026-10-01T00:00:00Z",
			"model_quotas":[{"public_model_key":"lazymind-text-default","capability":"llm","meter_unit":"token","periodic_quota":100}],
			"usage":[{"public_model_key":"lazymind-text-default","meter_unit":"token","periodic_quota":100,"used_amount":101,"remaining_amount":-1,"missing_usage_count":0}]
		}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, test.body, nil), nil
			})}
			client, err := New("https://cloud.example", httpClient)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.GetAccountTokenPlan(context.Background(), "fixture-access-token"); err == nil {
				t.Fatal("expected invalid Cloud payload to be rejected")
			} else if strings.Contains(err.Error(), "secret-canary") {
				t.Fatalf("validation error leaked payload: %v", err)
			}
		})
	}
}

func TestGetAccountTokenPlanRequiresBearerBeforeNetwork(t *testing.T) {
	called := false
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.GetAccountTokenPlan(context.Background(), " "); err == nil {
		t.Fatal("expected missing bearer to be rejected")
	}
	if called {
		t.Fatal("Cloud request must not start without an access token")
	}
}

func TestGetAccountTokenPlanAcceptsEveryPublishedRefreshCycle(t *testing.T) {
	for _, cycle := range []string{"monthly", "weekly", "fixed_7d"} {
		t.Run(cycle, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"status":"active",
				"plan_id":"00000000-0000-7000-8000-000000000701",
				"display_name":"Cycle Plan",
				"version":1,
				"refresh_cycle":%q,
				"next_refresh_at":"2026-09-14T10:27:45+08:00",
				"model_quotas":[{"public_model_key":"lazymind-text-default","capability":"llm","meter_unit":"token","periodic_quota":1000}],
				"usage":[{"public_model_key":"lazymind-text-default","meter_unit":"token","periodic_quota":1000,"used_amount":100,"remaining_amount":900,"missing_usage_count":0}]
			}`, cycle)
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, body, nil), nil
			})}
			client, err := New("https://cloud.example", httpClient)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := client.GetAccountTokenPlan(context.Background(), "fixture-access")
			if err != nil {
				t.Fatalf("published refresh cycle %q rejected: %v", cycle, err)
			}
			if plan.RefreshCycle != cycle {
				t.Fatalf("refresh cycle=%q want=%q", plan.RefreshCycle, cycle)
			}
		})
	}
}

func TestGetAccountTokenPlanRejectsUnpublishedRefreshCycleAlias(t *testing.T) {
	body := `{
		"status":"active",
		"plan_id":"00000000-0000-7000-8000-000000000701",
		"display_name":"Cycle Plan",
		"version":1,
		"refresh_cycle":"rolling_7d",
		"next_refresh_at":"2026-09-14T10:27:45+08:00",
		"model_quotas":[{"public_model_key":"lazymind-text-default","capability":"llm","meter_unit":"token","periodic_quota":1000}],
		"usage":[]
	}`
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, body, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetAccountTokenPlan(context.Background(), "fixture-access"); err == nil {
		t.Fatal("unpublished refresh-cycle alias accepted")
	}
}
