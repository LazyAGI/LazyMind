package modelprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func callBatchModelAdd(t *testing.T, parent, group, user string, models []addGroupModelRequest) (*httptest.ResponseRecorder, batchAddGroupModelsResponse) {
	t.Helper()
	raw, _ := json.Marshal(batchAddGroupModelsRequest{Models: models})
	rec := httptest.NewRecorder()
	BatchAddGroupModels(rec, batchModelRequest(parent, group, user, raw))
	var response struct {
		Data batchAddGroupModelsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return rec, response.Data
}

func TestBatchAddGroupModelsPreservesExistingAndRestoresUUID(t *testing.T) {
	db, parent, group, _ := batchModelFixture(t)
	manualContext, importedContext := "131073", "262144"
	deletedAt := time.Now()
	active := orm.UserModelProviderGroupModel{ID: "manual-unknown", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "active", ModelType: "", MaxInputTokens: &manualContext, BaseModel: parent.BaseModel}
	tombstone := orm.UserModelProviderGroupModel{ID: "restore-id", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "restore", ModelType: "llm", IsDefault: true, BaseModel: parent.BaseModel}
	tombstone.DeletedAt = &deletedAt
	unrequested := orm.UserModelProviderGroupModel{ID: "unrequested", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "other-saved-model", ModelType: "llm", BaseModel: parent.BaseModel}
	for _, row := range []any{&active, &tombstone, &unrequested} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	invalidContext := "invalid"
	models := []addGroupModelRequest{
		{Name: " active ", ModelType: "bad-stale-type", Vision: true, MaxInputTokens: &invalidContext},
		{Name: "fresh", ModelType: "llm", MaxInputTokens: &importedContext},
		{Name: " fresh ", ModelType: "embed"},
		{Name: "restore", ModelType: "vlm", Vision: true, MaxInputTokens: &importedContext},
		{Name: "invalid", ModelType: "future-task"},
		{Name: "retry", ModelType: "future-task"},
		{Name: "retry", ModelType: "", MaxInputTokens: &manualContext},
		{Name: " ", ModelType: "llm"},
	}
	rec, response := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, models)
	if rec.Code != http.StatusOK || response.AddedCount != 3 || response.ExistingCount != 1 || response.TotalCount != 5 || len(response.Models) != 4 || len(response.Failures) != 3 {
		t.Fatalf("mixed batch=%d %s", rec.Code, rec.Body.String())
	}
	for index, name := range []string{"active", "fresh", "restore", "retry"} {
		if response.Models[index].Name != name {
			t.Fatalf("response order does not follow unique valid input: %+v", response.Models)
		}
	}
	if result := response.Models[0]; result.ID != active.ID || result.ModelType != "" || result.Vision || tokenLimitString(result.MaxInputTokens) != manualContext {
		t.Fatalf("existing manual metadata overwritten: %+v", result)
	}
	if result := response.Models[2]; result.ID != tombstone.ID || result.ModelType != "vlm" || !result.Vision || result.IsDefault || tokenLimitString(result.MaxInputTokens) != importedContext {
		t.Fatalf("tombstone UUID/metadata not restored: %+v", result)
	}
	var saved orm.UserModelProviderGroupModel
	if err := db.Take(&saved, "id = ?", active.ID).Error; err != nil || saved.ModelType != "" || tokenLimitString(saved.MaxInputTokens) != manualContext {
		t.Fatalf("active row changed: %+v %v", saved, err)
	}
	rec, repeated := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, models)
	if rec.Code != http.StatusOK || repeated.AddedCount != 0 || repeated.ExistingCount != 4 || repeated.TotalCount != 5 || len(repeated.Models) != 4 {
		t.Fatalf("repeat batch is not idempotent=%d %s", rec.Code, rec.Body.String())
	}
	for index := range response.Models {
		if repeated.Models[index].ID != response.Models[index].ID {
			t.Fatal("repeated import changed a model UUID")
		}
	}
}

func TestBatchAddGroupModelsAllInvalidStillReturnsCurrentTotal(t *testing.T) {
	db, parent, group, _ := batchModelFixture(t)
	row := orm.UserModelProviderGroupModel{ID: "saved", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "saved", ModelType: "", BaseModel: parent.BaseModel}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	rec, response := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, []addGroupModelRequest{{Name: "invalid", ModelType: "bad-type"}})
	if rec.Code != http.StatusOK || response.TotalCount != 1 || response.AddedCount != 0 || response.ExistingCount != 0 || len(response.Models) != 0 || len(response.Failures) != 1 {
		t.Fatalf("all-invalid batch lost saved inventory count=%d %s", rec.Code, rec.Body.String())
	}
}

func TestBatchAddGroupModelsBoundsQueriesForLargeImport(t *testing.T) {
	db, parent, group, counter := batchModelFixture(t)
	models := make([]addGroupModelRequest, 1000)
	for index := range models {
		models[index] = addGroupModelRequest{Name: fmt.Sprintf("bulk-%04d", index), ModelType: ""}
	}
	rec, response := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, models)
	statements := counter.count.Load()
	if rec.Code != http.StatusOK || response.AddedCount != len(models) || len(response.Models) != len(models) || len(response.Failures) != 0 {
		t.Fatalf("large batch=%d %s", rec.Code, rec.Body.String())
	}
	if statements > 20 {
		t.Fatalf("import still performs per-model database work: %d SQL statements", statements)
	}
	var count int64
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("deleted_at IS NULL AND model_type = '' AND max_input_tokens IS NULL").Count(&count).Error; err != nil || count != int64(len(models)) {
		t.Fatalf("unknown batch guessed metadata or lost rows: count=%d %v", count, err)
	}
}

func TestBatchAddGroupModelsRejectsMalformedAndOversizedRequests(t *testing.T) {
	_, parent, group, _ := batchModelFixture(t)
	oversized := make([]addGroupModelRequest, 1001)
	tooMany, _ := json.Marshal(batchAddGroupModelsRequest{Models: oversized})
	for _, body := range []string{`{}`, `{"models":[]}`, `{"models":null}`, `{"models":[`, `{"models":[{"name":"ok","model_type":""}]} {}`, string(tooMany)} {
		rec := httptest.NewRecorder()
		BatchAddGroupModels(rec, batchModelRequest(parent.ID, group.ID, parent.CreateUserID, []byte(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid body returned=%d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestBatchAddGroupModelsKeepsTenantAndSupplierTypeValidation(t *testing.T) {
	for _, mode := range []string{"missing user", "foreign user", "foreign group", "no models", "unsupported protocol type"} {
		t.Run(mode, func(t *testing.T) {
			db, parent, group, _ := batchModelFixture(t)
			user, groupID, want := parent.CreateUserID, group.ID, http.StatusBadRequest
			models := []addGroupModelRequest{{Name: "new-model", ModelType: "embed"}}
			switch mode {
			case "missing user":
				user = ""
			case "foreign user":
				user, want = "other-owner", http.StatusNotFound
			case "foreign group":
				groupID, want = "other-group", http.StatusNotFound
			case "no models":
				if err := db.Model(&parent).Update("capabilities", "").Error; err != nil {
					t.Fatal(err)
				}
			case "unsupported protocol type":
				if err := db.Model(&parent).Update("protocol_name", "Claude").Error; err != nil {
					t.Fatal(err)
				}
				want = http.StatusOK
			}
			rec, response := callBatchModelAdd(t, parent.ID, groupID, user, models)
			if rec.Code != want || (mode == "unsupported protocol type" && (len(response.Failures) != 1 || response.AddedCount != 0)) {
				t.Fatalf("scope/type protection changed=%d %s", rec.Code, rec.Body.String())
			}
			var count int64
			if err := db.Model(&orm.UserModelProviderGroupModel{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("rejected batch created models", err)
			}
		})
	}
}

func TestBatchAddGroupModelsRollsBackAllChunksOnDatabaseFailure(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore=%v", restore), func(t *testing.T) {
			db, parent, group, _ := batchModelFixture(t)
			models := make([]addGroupModelRequest, 150)
			for index := range models {
				models[index] = addGroupModelRequest{Name: fmt.Sprintf("rollback-%04d", index), ModelType: "llm"}
				if restore {
					deletedAt := time.Now()
					row := orm.UserModelProviderGroupModel{ID: fmt.Sprintf("old-%04d", index), UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: models[index].Name, ModelType: "", BaseModel: parent.BaseModel}
					row.DeletedAt = &deletedAt
					if err := db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			var chunks atomic.Int32
			const callback = "test:fail-second-model-batch-chunk"
			if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "user_model_provider_group_models" && chunks.Add(1) == 2 {
					tx.AddError(errors.New("private database details"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Callback().Create().Remove(callback) })
			rec, _ := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, models)
			if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "private database") {
				t.Fatalf("database failure exposed or committed=%d %s", rec.Code, rec.Body.String())
			}
			var live int64
			if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("deleted_at IS NULL").Count(&live).Error; err != nil || live != 0 {
				t.Fatalf("partial chunks committed: live=%d %v", live, err)
			}
		})
	}
}

func TestBatchAddGroupModelsRechecksDeletedParentAndGroup(t *testing.T) {
	for _, target := range []string{"provider", "group"} {
		t.Run(target, func(t *testing.T) {
			db, parent, group, _ := batchModelFixture(t)
			var injected atomic.Bool
			const callback = "test:delete-before-model-batch-lock"
			if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "user_model_provider_groups" || !injected.CompareAndSwap(false, true) {
					return
				}
				row := any(&parent)
				if target == "group" {
					row = &group
				}
				if err := db.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Model(row).Update("deleted_at", time.Now()).Error; err != nil {
					t.Error(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Callback().Query().Remove(callback) })
			rec, _ := callBatchModelAdd(t, parent.ID, group.ID, parent.CreateUserID, []addGroupModelRequest{{Name: "new-model", ModelType: "llm"}})
			if rec.Code != http.StatusConflict || !injected.Load() {
				t.Fatalf("deleted supplier/group accepted import=%d %s", rec.Code, rec.Body.String())
			}
			var count int64
			if err := db.Model(&orm.UserModelProviderGroupModel{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("model written under deleted supplier/group", err)
			}
		})
	}
}
