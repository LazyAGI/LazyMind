package readingpolicy

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"time"
)

var ErrQuotaExceeded = errors.New("每日自动额度已用尽")

const QuotaErrorCode = "reading_quota_exhausted"

type Usage struct {
	Used  int64 `json:"used"`
	Limit int   `json:"limit"`
}

func DayStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
func NextDay(now time.Time) time.Time { return DayStart(now).Add(24 * time.Hour) }
func DailyLimit(p Policy, category string) int {
	if Category(category) == PDFParse {
		return p.Limits.DailyPDFDocuments
	}
	return p.Limits.DailyStudyDocuments
}
func UsageToday(ctx context.Context, db *gorm.DB, user string, p Policy) (map[string]Usage, error) {
	out := map[string]Usage{}
	for _, key := range Keys {
		var n int64
		if err := db.WithContext(ctx).Model(&orm.ReadingUsage{}).Where("user_id=? AND category=? AND charged_at>=?", user, key, DayStart(time.Now())).Count(&n).Error; err != nil {
			return nil, err
		}
		out[key] = Usage{n, DailyLimit(p, key)}
	}
	return out, nil
}

// Claim is called only after a cache miss and immediately before new paid work.
// The primary key deliberately excludes the date: retries and supplemental work
// on the same source never spend a second paper allowance, even tomorrow.
func Claim(ctx context.Context, db *gorm.DB, user, category, hash string) error {
	return claimAt(ctx, db, user, category, hash, time.Now().UTC())
}
func claimAt(ctx context.Context, db *gorm.DB, user, category, hash string, now time.Time) error {
	if !IsAutomatic(ctx) {
		return nil
	}
	category = Category(category)
	if user == "" || hash == "" || (category != PDFParse && category != AIStudy) {
		return errors.New("无效的自动额度身份")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// An UPDATE before any reads takes the per-user write lock on PostgreSQL and
		// SQLite alike. Save and parallel workers serialize on this same policy row.
		locked := tx.Model(&orm.UserReadingPolicy{}).Where("user_id=?", user).UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return errors.New("自动准备策略不存在")
		}
		p, err := Load(ctx, tx, user)
		if err != nil {
			return err
		}
		if Mode(p, category) != Automatic {
			return fmt.Errorf("%s自动准备已停止", category)
		}
		var existing int64
		if err := tx.Model(&orm.ReadingUsage{}).Where("user_id=? AND category=? AND source_sha256=?", user, category, hash).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}

		var used int64
		if err := tx.Model(&orm.ReadingUsage{}).Where("user_id=? AND category=? AND charged_at>=? AND charged_at<?", user, category, DayStart(now), NextDay(now)).Count(&used).Error; err != nil {
			return err
		}
		if used >= int64(DailyLimit(p, category)) {
			name := "PDF 解析"
			if category == AIStudy {
				name = "AI 论文精读"
			}
			return fmt.Errorf("%w：%s今日已用 %d/%d 篇，将于次日自动继续", ErrQuotaExceeded, name, used, DailyLimit(p, category))
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&orm.ReadingUsage{UserID: user, Category: category, SourceSHA256: hash, ChargedAt: now}).Error
	})
}
