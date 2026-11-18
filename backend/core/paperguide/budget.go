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

// ModelAttemptTimeout is the server-owned per-attempt limit for Guide tasks.
const ModelAttemptTimeout = 120 * time.Second

var ErrBudget = errors.New("paper_guide_budget_exhausted")

func stringInt(n int64) string { return strconv.FormatInt(n, 10) }
