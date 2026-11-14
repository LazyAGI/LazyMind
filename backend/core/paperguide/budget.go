package paperguide

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"strconv"
)

const (
	MaxCalls         = 24
	MaxExtractCalls  = 8
	MaxRecoveryCalls = 2
	MaxActiveMillis  = int64(30 * 60 * 1000)
)

var ErrBudget = errors.New("paper_guide_budget_exhausted")

func stringInt(n int64) string { return strconv.FormatInt(n, 10) }

// Reserve commits before dispatch. Unknown/crashed calls are never refunded.
func (r Repository) Reserve(ctx context.Context, f asyncjob.FencedReporter, id string, epoch int64, kind string, recovery bool) error {
	return r.fenced(ctx, f, id, epoch, func(tx *gorm.DB, rev *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		if rev.CallsUsed >= MaxCalls || rev.ActiveMillis >= MaxActiveMillis || kind == "extract" && !recovery && rev.ExtractCalls >= MaxExtractCalls || recovery && rev.RecoveryCalls >= MaxRecoveryCalls || !recovery && kind != "extract" && rev.PlannedCalls >= 14 {
			return ErrBudget
		}
		updates := map[string]any{"calls_used": gorm.Expr("calls_used + 1")}
		if !recovery && kind != "extract" {
			updates["planned_calls"] = gorm.Expr("planned_calls + 1")
		}
		if kind == "extract" && !recovery {
			updates["extract_calls"] = gorm.Expr("extract_calls + 1")
		}
		if recovery {
			updates["recovery_calls"] = gorm.Expr("recovery_calls + 1")
		}
		return tx.Model(rev).Updates(updates).Error
	})
}
