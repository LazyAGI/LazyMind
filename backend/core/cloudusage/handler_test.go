package cloudusage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/cloudclient"
)

type fakeSource struct {
	plan cloudclient.AccountTokenPlan
	err  error
}

func (f fakeSource) Get(context.Context) (cloudclient.AccountTokenPlan, error) {
	return f.plan, f.err
}

func TestHandlerReturnsTheBoundedReadOnlyPlanProjection(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/cloud/token-plan", nil)
	Handler{Source: fakeSource{plan: cloudclient.AccountTokenPlan{
		Status: "active", DisplayName: "Free Plan", Version: 3, RefreshCycle: "monthly",
		NextRefreshAt: "2026-10-01T00:00:00Z",
		ModelQuotas: []cloudclient.TokenPlanModelQuota{{
			PublicModelKey: "lazymind-text-default", Capability: "llm", MeterUnit: "token", PeriodicQuota: 10000,
		}},
		Usage: []cloudclient.TokenPlanPeriodUsage{{
			PublicModelKey: "lazymind-text-default", MeterUnit: "token", PeriodicQuota: 10000,
			UsedAmount: 2500, RemainingAmount: 7500, MissingUsageCount: 2,
		}},
	}}}.Get(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data cloudclient.AccountTokenPlan `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Usage[0].RemainingAmount != 7500 {
		t.Fatalf("data=%+v", response.Data)
	}
	for _, forbidden := range []string{
		"plan_id", "display_name", "version", "refresh_cycle", "next_refresh_at",
		"access_token", "refresh_token", "cookie", "upstream_provider", "price", "currency", "assignment_source",
	} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("response leaked forbidden field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestHandlerMapsSessionAndCloudFailuresWithoutLeakingDetails(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "signed out", err: ErrCloudSessionUnavailable, wantStatus: http.StatusUnauthorized},
		{name: "Cloud forbidden", err: &cloudclient.CloudError{HTTPStatus: http.StatusForbidden, Message: "secret-canary"}, wantStatus: http.StatusForbidden},
		{name: "Cloud rate limited", err: &cloudclient.CloudError{HTTPStatus: http.StatusTooManyRequests, Message: "secret-canary"}, wantStatus: http.StatusTooManyRequests},
		{name: "dependency failure", err: errors.New("upstream failed with secret-canary"), wantStatus: http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			Handler{Source: fakeSource{err: test.err}}.Get(recorder, httptest.NewRequest(http.MethodGet, "/cloud/token-plan", nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret-canary") {
				t.Fatalf("unsafe error response: %s", recorder.Body.String())
			}
		})
	}
}

func TestHandlerFailsSafelyWhenTheSourceIsUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler{}.Get(recorder, httptest.NewRequest(http.MethodGet, "/cloud/token-plan", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerUsesTheDedicatedCloudSessionCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler{Source: fakeSource{err: ErrCloudSessionUnavailable}}.Get(
		recorder,
		httptest.NewRequest(http.MethodGet, "/cloud/token-plan", nil),
	)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 2002920 {
		t.Fatalf("Cloud session code=%d want=2002920 body=%s", response.Code, recorder.Body.String())
	}
}
