package cloudclient

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

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
