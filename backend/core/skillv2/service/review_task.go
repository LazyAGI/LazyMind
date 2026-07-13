package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const skillReviewStatsStatusRunning = "running"

func (s *SkillService) HasRunningSkillReviewTask(ctx context.Context, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, fmt.Errorf("user_id is required")
	}

	var row struct {
		ID string `gorm:"column:id"`
	}
	err := s.db.WithContext(ctx).
		Table("skill_review_stats").
		Select("id").
		Where("userid = ? AND status = ?", userID, skillReviewStatsStatusRunning).
		Order("started_at DESC, id DESC").
		Take(&row).Error
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}
