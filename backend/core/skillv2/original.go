package skillv2

import (
	"context"
	"gorm.io/gorm"
)

// EnsureOriginalRevision initializes the immutable pointer from the earliest
// retained revision. A current head must never substitute for known history.
func EnsureOriginalRevision(ctx context.Context, tx *gorm.DB, skillID string) error {
	return tx.WithContext(ctx).Table("skills").Where("id = ? AND original_revision_id IS NULL", skillID).
		Update("original_revision_id", gorm.Expr("(SELECT id FROM skill_revisions WHERE skill_id = ? ORDER BY revision_no ASC, created_at ASC, id ASC LIMIT 1)", skillID)).Error
}
