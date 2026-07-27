package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"lazymind/core/common"
)

func notifyToolLimitDecision(convID, decisionID, action string) error {
	body, _ := json.Marshal(map[string]string{
		"conversation_id": convID,
		"decision_id":     decisionID,
		"action":          action,
	})
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/agent/tool-limit-decision")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("chat service returned status %d", resp.StatusCode)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("tool-limit decision is no longer active")
	}
	return nil
}
