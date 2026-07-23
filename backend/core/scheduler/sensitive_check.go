package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common"
)

func validateScheduleDescription(ctx context.Context, description string) error {
	body, _ := json.Marshal(map[string]string{"text": description})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/sensitive-check"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sensitive-word check unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sensitive-word check failed: status %d", resp.StatusCode)
	}
	var result struct {
		Passed      bool   `json:"passed"`
		MatchedWord string `json:"matched_word"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode sensitive-word check: %w", err)
	}
	if !result.Passed {
		if strings.TrimSpace(result.MatchedWord) == "" {
			return fmt.Errorf("task description contains sensitive content")
		}
		return fmt.Errorf("task description contains sensitive word: %s", result.MatchedWord)
	}
	return nil
}
