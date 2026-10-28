package modelprovider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const maxBatchGroupModels = 1000
const batchGroupModelChunkSize = 100
const batchGroupModelBodyLimit = 4 << 20

var errBatchSupplierHasNoModels = errors.New("this provider does not support models")

type batchAddGroupModelsRequest struct {
	Models []addGroupModelRequest `json:"models"`
}

type batchAddGroupModelFailure struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

type batchAddGroupModelsResponse struct {
	Models        []addGroupModelResponse     `json:"models"`
	AddedCount    int                         `json:"added_count"`
	ExistingCount int                         `json:"existing_count"`
	TotalCount    int64                       `json:"total_count"`
	Failures      []batchAddGroupModelFailure `json:"failures"`
}

// BatchAddGroupModels imports one catalog selection under a single supplier
// lock and transaction. Existing live models remain the user's configuration.
func BatchAddGroupModels(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	parentID, groupID := strings.TrimSpace(mux.Vars(r)["model_provider_id"]), strings.TrimSpace(mux.Vars(r)["group_id"])
	if parentID == "" || groupID == "" {
		common.ReplyErr(w, "missing model_provider_id or group_id", http.StatusBadRequest)
		return
	}
	var req batchAddGroupModelsRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, batchGroupModelBodyLimit))
	if err := decoder.Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(req.Models) == 0 || len(req.Models) > maxBatchGroupModels {
		common.ReplyErr(w, "models must contain between 1 and 1000 entries", http.StatusBadRequest)
		return
	}
	parent, group, ok := loadModelWriteGroup(w, r, db, userID, parentID, groupID)
	if !ok {
		return
	}
	response := batchAddGroupModelsResponse{Models: []addGroupModelResponse{}, Failures: []batchAddGroupModelFailure{}}
	userName := strings.TrimSpace(store.UserName(r))
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &parent); err != nil {
			return errConnectionChanged
		}
		if !parent.HasCapability("has_models") {
			return errBatchSupplierHasNoModels
		}
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, parent.ID, userID).Take(&group).Error; err != nil {
			return errConnectionChanged
		}
		names := make([]string, 0, len(req.Models))
		nameSet := make(map[string]bool, len(req.Models))
		for _, model := range req.Models {
			name := strings.TrimSpace(model.Name)
			if validateModelName(name) == nil && !nameSet[name] {
				names = append(names, name)
				nameSet[name] = true
			}
		}
		var saved []orm.UserModelProviderGroupModel
		if len(names) > 0 {
			if err := tx.Where("user_model_provider_id = ? AND user_model_provider_group_id = ? AND create_user_id = ? AND name IN ?", parent.ID, group.ID, userID, names).Find(&saved).Error; err != nil {
				return err
			}
		}
		byName := make(map[string]orm.UserModelProviderGroupModel, len(saved))
		for _, model := range saved {
			byName[model.Name] = model
		}
		accepted := make(map[string]bool, len(req.Models))
		created, restored := []orm.UserModelProviderGroupModel{}, []orm.UserModelProviderGroupModel{}
		now := time.Now()
		for _, model := range req.Models {
			name := strings.TrimSpace(model.Name)
			if accepted[name] {
				continue
			}
			previous, exists := byName[name]
			if exists && previous.DeletedAt == nil {
				response.Models = append(response.Models, groupModelAdditionResponse(previous, group))
				response.ExistingCount++
				accepted[name] = true
				continue
			}
			candidate, err := prepareGroupModelAddition(model)
			if err == nil {
				err = validateSupplierModelType(r.Context(), tx, parent, candidate.ModelType)
			}
			if err != nil {
				response.Failures = append(response.Failures, batchAddGroupModelFailure{Name: name, Message: err.Error()})
				continue
			}
			candidate.UserModelProviderID, candidate.UserModelProviderGroupID = parent.ID, group.ID
			candidate.ProviderName = RuntimeProviderName(parent)
			candidate.BaseModel = orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}
			if exists {
				candidate.ID, candidate.CreatedAt, candidate.CreateUserName = previous.ID, previous.CreatedAt, previous.CreateUserName
				candidate.FreeAutoSelectPriority, candidate.FreeAutoSelectBaseURLs = previous.FreeAutoSelectPriority, previous.FreeAutoSelectBaseURLs
				restored = append(restored, candidate)
			} else {
				candidate.ID = common.GenerateID()
				created = append(created, candidate)
			}
			response.Models = append(response.Models, groupModelAdditionResponse(candidate, group))
			response.AddedCount++
			accepted[name] = true
		}
		if len(created) > 0 {
			if err := tx.CreateInBatches(&created, batchGroupModelChunkSize).Error; err != nil {
				return err
			}
		}
		if len(restored) > 0 {
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"provider_name", "model_type", "vision", "model_settings", "max_input_tokens", "input_token_limit", "max_output_tokens", "is_default", "updated_at", "deleted_at"}),
				Where: clause.Where{Exprs: []clause.Expression{
					clause.Neq{Column: clause.Column{Table: clause.CurrentTable, Name: "deleted_at"}, Value: nil},
					clause.Eq{Column: clause.Column{Table: clause.CurrentTable, Name: "create_user_id"}, Value: userID},
					clause.Eq{Column: clause.Column{Table: clause.CurrentTable, Name: "user_model_provider_id"}, Value: parent.ID},
					clause.Eq{Column: clause.Column{Table: clause.CurrentTable, Name: "user_model_provider_group_id"}, Value: group.ID},
				}},
			}).CreateInBatches(&restored, batchGroupModelChunkSize)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(len(restored)) {
				return errConnectionChanged
			}
		}
		return tx.Model(&orm.UserModelProviderGroupModel{}).
			Where("user_model_provider_id = ? AND user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, group.ID, userID).
			Count(&response.TotalCount).Error
	})
	if err != nil {
		if errors.Is(err, errConnectionChanged) {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
		} else if errors.Is(err, errBatchSupplierHasNoModels) {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		} else {
			common.ReplyErr(w, "save models failed", http.StatusInternalServerError)
		}
		return
	}
	common.ReplyOK(w, response)
}
