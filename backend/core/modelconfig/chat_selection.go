package modelconfig

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"math/rand/v2"
	"sort"
	"strings"
)

func FindChatModel(models []AvailableChatModel, modelID, source string) *AvailableChatModel {
	modelID = strings.TrimSpace(modelID)
	source = strings.ToLower(strings.TrimSpace(source))
	for index := range models {
		if models[index].ID != modelID {
			continue
		}
		if source == "" && models[index].Source != "cloud" || models[index].Source == source {
			return &models[index]
		}
	}
	return nil
}

func ChatModelUsable(model *AvailableChatModel) bool {
	return model != nil && (model.Availability == "" || model.Availability == "available" || model.Availability == "degraded")
}

func ChatModelSelectable(model *AvailableChatModel) bool {
	return ChatModelUsable(model) && (model.Lifecycle == "" || model.Lifecycle == "active")
}

func ResolveOwnDefaultChatModel(ctx context.Context, db *gorm.DB, userID string, models []AvailableChatModel) (*AvailableChatModel, error) {
	type selectedID struct {
		ModelID string `gorm:"column:model_id"`
	}
	var own []selectedID
	if err := db.WithContext(ctx).
		Table("user_selected_models").
		Select("user_model_provider_group_model_id AS model_id").
		Where("user_id = ? AND model_type = ?", strings.TrimSpace(userID), "llm").
		Order("updated_at DESC").
		Scan(&own).Error; err != nil {
		return nil, err
	}
	for _, item := range own {
		if model := FindChatModel(models, item.ModelID, "own"); ChatModelUsable(model) {
			return model, nil
		}
	}
	return nil, nil
}

func ResolveDefaultChatModel(ctx context.Context, db *gorm.DB, userID string, models []AvailableChatModel) (*AvailableChatModel, error) {
	if db.Migrator().HasTable(&orm.UserSelectedCloudModel{}) {
		var selected orm.UserSelectedCloudModel
		err := db.WithContext(ctx).
			Where("user_id = ? AND model_type = ?", strings.TrimSpace(userID), "llm").
			Take(&selected).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			if model := FindChatModel(models, selected.PublicModelKey, "cloud"); ChatModelUsable(model) {
				return model, nil
			}
		}
	}
	ownDefault, err := ResolveOwnDefaultChatModel(ctx, db, userID, models)
	if err != nil || ownDefault != nil {
		return ownDefault, err
	}
	type selectedID struct {
		ModelID string `gorm:"column:model_id"`
	}

	var shared []selectedID
	if err := db.WithContext(ctx).
		Table("user_selected_models").
		Select("user_model_provider_group_model_id AS model_id").
		Where("model_type = ? AND share = ?", "llm", true).
		Order("updated_at DESC").
		Scan(&shared).Error; err != nil {
		return nil, err
	}
	for _, item := range shared {
		if model := FindChatModel(models, item.ModelID, "shared"); ChatModelUsable(model) {
			return model, nil
		}
	}
	for index := range models {
		if models[index].Source == "cloud" && models[index].DefaultForType && ChatModelSelectable(&models[index]) {
			return &models[index], nil
		}
	}
	for index := range models {
		if models[index].Source == "cloud" && ChatModelSelectable(&models[index]) {
			return &models[index], nil
		}
	}
	return nil, nil
}

func InitialChatCandidate(prepared *ChatRuntimeConfig, originalWindow *int64) (*AvailableChatModel, *ChatModelCandidate) {
	candidates := append([]ChatModelCandidate(nil), prepared.Candidates...)
	rank := func(candidate ChatModelCandidate) (int, int64) {
		size := candidate.MaxInputTokens
		if size == nil {
			return 3, 0
		}
		if originalWindow == nil {
			return 0, -*size
		}
		if *size == *originalWindow {
			return 0, 0
		}
		if *size > *originalWindow {
			return 1, *size
		}
		return 2, -*size
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, x := rank(candidates[i])
		b, y := rank(candidates[j])
		return a < b || a == b && x < y
	})
	// An unavailable initial model may leave only unknown declarations. Choose
	// its first replacement uniformly; the Python run shuffles remaining unknowns
	// once and never re-enqueues the selected model after it fails.
	if len(candidates) > 1 && candidates[0].MaxInputTokens == nil {
		selected := rand.IntN(len(candidates))
		candidates[0], candidates[selected] = candidates[selected], candidates[0]
	}
	for _, candidate := range candidates {
		for i := range prepared.Models {
			model := &prepared.Models[i]
			if ChatCandidateID(model.Source, model.ID) == candidate.CandidateID {
				for j := range prepared.Candidates {
					if prepared.Candidates[j].CandidateID == candidate.CandidateID {
						return model, &prepared.Candidates[j]
					}
				}
			}
		}
	}
	return nil, nil
}
