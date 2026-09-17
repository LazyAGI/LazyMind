package cloudclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const maxTokenPlanAmount int64 = 9007199254740991

type TokenPlanModelQuota struct {
	PublicModelKey string `json:"public_model_key"`
	Capability     string `json:"capability"`
	MeterUnit      string `json:"meter_unit"`
	PeriodicQuota  int64  `json:"periodic_quota"`
}

type TokenPlanPeriodUsage struct {
	PublicModelKey    string `json:"public_model_key"`
	MeterUnit         string `json:"meter_unit"`
	PeriodicQuota     int64  `json:"periodic_quota"`
	UsedAmount        int64  `json:"used_amount"`
	RemainingAmount   int64  `json:"remaining_amount"`
	MissingUsageCount int64  `json:"missing_usage_count"`
}

type AccountTokenPlan struct {
	Status        string                 `json:"status"`
	PlanID        string                 `json:"plan_id,omitempty"`
	DisplayName   string                 `json:"display_name,omitempty"`
	Version       int64                  `json:"version,omitempty"`
	RefreshCycle  string                 `json:"refresh_cycle,omitempty"`
	NextRefreshAt string                 `json:"next_refresh_at,omitempty"`
	ModelQuotas   []TokenPlanModelQuota  `json:"model_quotas,omitempty"`
	Usage         []TokenPlanPeriodUsage `json:"usage,omitempty"`
}

func (c *Client) GetAccountTokenPlan(ctx context.Context, accessToken string) (AccountTokenPlan, error) {
	if err := validateBearer(accessToken); err != nil {
		return AccountTokenPlan{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/account/token-plan"), nil)
	if err != nil {
		return AccountTokenPlan{}, err
	}
	setCloudHeaders(request, accessToken)
	var plan AccountTokenPlan
	if err := c.doJSON(request, http.StatusOK, &plan, "decode LazyMind Cloud account Token Plan"); err != nil {
		return AccountTokenPlan{}, err
	}
	if err := validateAccountTokenPlan(plan); err != nil {
		return AccountTokenPlan{}, err
	}
	return plan, nil
}

func validateAccountTokenPlan(plan AccountTokenPlan) error {
	if plan.Status == "inactive" {
		if plan.PlanID != "" || plan.DisplayName != "" || plan.Version != 0 || plan.RefreshCycle != "" || plan.NextRefreshAt != "" ||
			len(plan.ModelQuotas) != 0 || len(plan.Usage) != 0 {
			return errors.New("LazyMind Cloud returned an invalid inactive Token Plan")
		}
		return nil
	}
	if plan.Status != "active" || !isSafeCloudID(plan.PlanID) || strings.TrimSpace(plan.DisplayName) == "" || len(plan.DisplayName) > 128 ||
		plan.Version < 1 || !validTokenPlanRefreshCycle(plan.RefreshCycle) || len(plan.ModelQuotas) < 1 || len(plan.ModelQuotas) > 100 || len(plan.Usage) > 100 {
		return errors.New("LazyMind Cloud returned an invalid account Token Plan")
	}
	if _, err := time.Parse(time.RFC3339, plan.NextRefreshAt); err != nil {
		return errors.New("LazyMind Cloud returned an invalid Token Plan refresh time")
	}

	quotas := make(map[string]TokenPlanModelQuota, len(plan.ModelQuotas))
	for _, quota := range plan.ModelQuotas {
		if !validPublicModelKey(quota.PublicModelKey) || !validTokenPlanCapability(quota.Capability) || !validTokenPlanMeterUnit(quota.MeterUnit) ||
			quota.PeriodicQuota < 1 || quota.PeriodicQuota > maxTokenPlanAmount {
			return errors.New("LazyMind Cloud returned an invalid Token Plan quota")
		}
		if _, exists := quotas[quota.PublicModelKey]; exists {
			return errors.New("LazyMind Cloud returned duplicate Token Plan quotas")
		}
		quotas[quota.PublicModelKey] = quota
	}

	seenUsage := make(map[string]struct{}, len(plan.Usage))
	for _, usage := range plan.Usage {
		quota, exists := quotas[usage.PublicModelKey]
		if !exists || usage.MeterUnit != quota.MeterUnit || usage.PeriodicQuota != quota.PeriodicQuota ||
			usage.UsedAmount < 0 || usage.RemainingAmount < 0 || usage.MissingUsageCount < 0 ||
			usage.PeriodicQuota < 1 || usage.PeriodicQuota > maxTokenPlanAmount || usage.UsedAmount > maxTokenPlanAmount || usage.RemainingAmount > maxTokenPlanAmount {
			return errors.New("LazyMind Cloud returned invalid Token Plan usage")
		}
		if _, exists := seenUsage[usage.PublicModelKey]; exists {
			return errors.New("LazyMind Cloud returned duplicate Token Plan usage")
		}
		seenUsage[usage.PublicModelKey] = struct{}{}
	}
	return nil
}

func validTokenPlanRefreshCycle(value string) bool {
	return value == "monthly" || value == "weekly" || value == "fixed_7d"
}

func validTokenPlanCapability(value string) bool {
	switch value {
	case "llm", "evo_llm", "vlm", "embed_main", "embed_image", "reranker", "text2image", "image_editing", "text2video", "stt", "tts":
		return true
	default:
		return false
	}
}

func validTokenPlanMeterUnit(value string) bool {
	switch value {
	case "token", "image", "second", "item", "document":
		return true
	default:
		return false
	}
}
