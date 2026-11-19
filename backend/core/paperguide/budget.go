package paperguide

import (
	"errors"
	"strconv"
	"time"
)

const (
	MaxCalls        = 24
	MaxActiveMillis = int64(30 * 60 * 1000)

	localProcessingReserveMillis = int64(120000)
)

func ModelAttemptTimeout(task string) time.Duration {
	if task == "paper.explainer.chapter" {
		return 5 * time.Minute
	}
	return 2 * time.Minute
}

var ErrBudget = errors.New("paper_guide_budget_exhausted")

func stringInt(n int64) string { return strconv.FormatInt(n, 10) }
