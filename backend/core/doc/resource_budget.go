package doc

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// A deployment may enable cross-library moves only with measured limits. The
// absence of this configuration keeps the cross-library operation closed.
func KnowledgeMoveBudget() (ResourceBudget, error) {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON"))
	if raw == "" {
		return ResourceBudget{}, nil
	}
	var config struct {
		MaxResources       int   `json:"max_resources"`
		MaxBytes           int64 `json:"max_bytes"`
		PrepareSeconds     int   `json:"prepare_seconds"`
		CopyBytesPerSecond int64 `json:"copy_bytes_per_second"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ResourceBudget{}, fmt.Errorf("invalid knowledge move budget: %w", err)
	}
	if config.MaxResources < 1 || config.MaxBytes < 1 || config.PrepareSeconds < 4 || config.PrepareSeconds > 300 || config.CopyBytesPerSecond < 1 {
		return ResourceBudget{}, fmt.Errorf("invalid knowledge move budget limits")
	}
	return ResourceBudget{Calibrated: true, MaxResources: config.MaxResources, MaxBytes: config.MaxBytes, MaxPrepareDuration: time.Duration(config.PrepareSeconds) * time.Second, CopyBytesPerSecond: config.CopyBytesPerSecond}, nil
}
