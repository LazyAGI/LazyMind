package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"

	"gorm.io/gorm"
)

const promptCategoryNameMaxLen = 30

type promptCategoryResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type promptCategoryRequest struct {
	Name string `json:"name"`
}

func promptCategoryFromPath(r *http.Request) string {
	name := common.PathVar(r, "name")
	return strings.TrimPrefix(strings.TrimPrefix(name, "prompt_categories/"), "/")
}

func listPromptCategories(userID string) ([]promptCategoryResponse, error) {
	var categories []orm.PromptCategory
	if err := corestore.DB().Where("create_user_id = ?", userID).Order("name ASC").Find(&categories).Error; err != nil {
		return nil, err
	}
	result := make([]promptCategoryResponse, 0, len(categories))
	for _, category := range categories {
		result = append(result, promptCategoryResponse{ID: category.ID, Name: category.Name})
	}
	return result, nil
}

func ListPromptCategories(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	categories, err := listPromptCategories(userID)
	if err != nil {
		common.ReplyErr(w, "query prompt categories failed", http.StatusInternalServerError)
		return
	}
	writePromptJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func CreatePromptCategory(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var body promptCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		common.ReplyErr(w, "name required", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(name) > promptCategoryNameMaxLen {
		common.ReplyErr(w, "category name too long", http.StatusBadRequest)
		return
	}
	var existing int64
	if err := corestore.DB().Model(&orm.PromptCategory{}).
		Where("create_user_id = ? AND lower(name) = lower(?)", userID, name).
		Count(&existing).Error; err != nil {
		common.ReplyErr(w, "query prompt categories failed", http.StatusInternalServerError)
		return
	}
	if existing > 0 {
		common.ReplyErr(w, "prompt category already exists", http.StatusConflict)
		return
	}
	now := time.Now().UTC()
	category := orm.PromptCategory{
		ID:   newID("pc_"),
		Name: name,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: corestore.UserName(r),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := corestore.DB().Create(&category).Error; err != nil {
		common.ReplyErr(w, "create prompt category failed", http.StatusConflict)
		return
	}
	writePromptJSON(w, http.StatusOK, promptCategoryResponse{ID: category.ID, Name: category.Name})
}

func DeletePromptCategory(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	categoryID := promptCategoryFromPath(r)
	if categoryID == "" {
		common.ReplyErr(w, "category id required", http.StatusBadRequest)
		return
	}
	err := corestore.DB().Transaction(func(tx *gorm.DB) error {
		var category orm.PromptCategory
		if err := tx.Where("id = ? AND create_user_id = ?", categoryID, userID).First(&category).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.Prompt{}).
			Where("category = ? AND create_user_id = ?", categoryID, userID).
			Update("category", "custom").Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&category).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "prompt category not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "delete prompt category failed", http.StatusInternalServerError)
		return
	}
	writePromptJSON(w, http.StatusOK, nil)
}
