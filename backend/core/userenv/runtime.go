package userenv

import (
	"context"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func LoadEnabled(ctx context.Context, db *gorm.DB, userID string) (map[string]string, error) {
	userID = strings.TrimSpace(userID)
	if db == nil || userID == "" {
		return nil, nil
	}
	var rows []orm.UserEnvironmentVariable
	if err := db.WithContext(ctx).
		Where("user_id = ? AND enabled = ?", userID, true).
		Order("name ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(rows))
	for _, row := range rows {
		if _, err := NormalizeName(row.Name); err != nil {
			return nil, common.ResolveAppError(err.Error(), http.StatusConflict).WithDetail(map[string]string{
				"reason": "user_env_invalid_name", "name": row.Name,
			})
		}
		value, err := UpgradeCredential(db.WithContext(ctx), &row)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) != "" {
			env[row.Name] = value
		}
	}
	return env, nil
}
