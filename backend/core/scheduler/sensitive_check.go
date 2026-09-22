package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"lazymind/core/common"
)

func validateScheduleDescription(ctx context.Context, description string) *common.AppError {
	unavailable := common.NewAppError(http.StatusServiceUnavailable, 2003105, "Task description check is unavailable; please try again")
	body, _ := json.Marshal(map[string]string{"text": description})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/sensitive-check"), bytes.NewReader(body))
	if err != nil {
		return unavailable
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return unavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return unavailable
	}
	// Only the explicit decision is needed. Match metadata may be an object,
	// a legacy string, or null and must never be echoed to the client.
	var result struct {
		Passed *bool `json:"passed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Passed == nil {
		return unavailable
	}
	if !*result.Passed {
		return common.NewAppError(http.StatusBadRequest, 2003104, "Task description contains sensitive content; please edit it before saving")
	}
	return nil
}
