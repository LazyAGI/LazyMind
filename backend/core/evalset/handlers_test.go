package evalset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func newEvalSetTestDB(t *testing.T) *orm.DB {
	t.Helper()

	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://%")
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, db.DB, nil)
	acl.InitStore(db)
	seedEvalSetShard(t, db, DefaultShardID, 0, 0)
	return db
}

func seedEvalSetShard(t *testing.T, db *orm.DB, id string, actualRows, estimatedBytes int64) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.EvalSetShard{
		ID:                     id,
		Status:                 ShardStatusOpen,
		RowLimit:               DefaultShardRowLimit,
		RowOpenThreshold:       DefaultShardRowOpenThreshold,
		SizeLimitBytes:         DefaultShardSizeLimitBytes,
		SizeOpenThresholdBytes: DefaultShardSizeOpenThreshold,
		ActualRows:             actualRows,
		EstimatedBytes:         estimatedBytes,
		CreatedAt:              now,
		UpdatedAt:              now,
	}).Error; err != nil {
		t.Fatalf("seed shard: %v", err)
	}
}

func seedEvalSet(t *testing.T, db *orm.DB, id, ownerID, groupID, datasetID string, updatedAt time.Time) orm.EvalSet {
	t.Helper()
	datasetIDs := []string{"dataset_default"}
	if strings.TrimSpace(datasetID) != "" {
		datasetIDs = strings.Split(datasetID, ",")
	}
	row := orm.EvalSet{
		ID:             id,
		Name:           id,
		Description:    "description " + id,
		DatasetIDs:     datasetIDsJSON(datasetIDs),
		OwnerID:        ownerID,
		GroupID:        groupID,
		ShardID:        DefaultShardID,
		Status:         StatusActive,
		ItemCount:      0,
		CreateUserID:   ownerID,
		CreateUserName: ownerID + " name",
		CreatedAt:      updatedAt.Add(-time.Hour),
		UpdatedAt:      updatedAt,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed eval set %s: %v", id, err)
	}
	return row
}

func requestWithUser(method, target, body, userID string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
		req.Header.Set("X-User-Name", userID+" name")
	}
	rec := httptest.NewRecorder()
	return rec, req
}

func decodeOKData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope common.APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Code != common.CodeOK {
		t.Fatalf("expected code 0, got %d message=%s", envelope.Code, envelope.Message)
	}
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode data: %v; raw=%s", err, string(raw))
	}
	return out
}

func TestCreateEvalSetRequiresUser(t *testing.T) {
	newEvalSetTestDB(t)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets", `{"name":"cases"}`, "")
	CreateEvalSet(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateEvalSetWritesOwnerACL(t *testing.T) {
	db := newEvalSetTestDB(t)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets", `{"name":"cases","description":"desc","dataset_ids":["dataset_1","dataset_2"],"group_id":"group_1"}`, "owner_1")
	CreateEvalSet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[EvalSetResponse](t, rec)
	if !strings.HasPrefix(resp.ID, "eval_set_") {
		t.Fatalf("expected eval_set_ id, got %q", resp.ID)
	}
	if resp.ShardID == "" {
		t.Fatalf("expected non-empty shard_id")
	}
	if strings.Join(resp.DatasetIDs, ",") != "dataset_1,dataset_2" {
		t.Fatalf("unexpected dataset_ids: %#v", resp.DatasetIDs)
	}

	var ownerRows []orm.ACLModel
	if err := db.Where("resource_type = ? AND resource_id = ? AND grantee_type = ? AND target_id = ?", acl.ResourceTypeEvalSet, resp.ID, acl.GranteeUser, "owner_1").
		Find(&ownerRows).Error; err != nil {
		t.Fatalf("query owner acl: %v", err)
	}
	if got := permissionsFromACL(ownerRows); strings.Join(got, ",") != "EVAL_SET_READ,EVAL_SET_WRITE" {
		t.Fatalf("expected owner read/write ACL, got %v", got)
	}

	var groupRows []orm.ACLModel
	if err := db.Where("resource_type = ? AND resource_id = ? AND grantee_type = ? AND target_id = ?", acl.ResourceTypeEvalSet, resp.ID, acl.GranteeGroup, "group_1").
		Find(&groupRows).Error; err != nil {
		t.Fatalf("query group acl: %v", err)
	}
	if got := permissionsFromACL(groupRows); strings.Join(got, ",") != "EVAL_SET_READ,EVAL_SET_WRITE" {
		t.Fatalf("expected group read/write ACL, got %v", got)
	}
}

func TestCreateEvalSetAllowsEmptyDatasetIDs(t *testing.T) {
	db := newEvalSetTestDB(t)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets", `{"name":"cases without kb"}`, "owner_1")
	CreateEvalSet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[EvalSetResponse](t, rec)
	if len(resp.DatasetIDs) != 0 || len(resp.DatasetNames) != 0 {
		t.Fatalf("expected no bound datasets, got %#v", resp)
	}

	var row orm.EvalSet
	if err := db.First(&row, "id = ?", resp.ID).Error; err != nil {
		t.Fatalf("query created eval set: %v", err)
	}
	if got := parseDatasetIDsJSON(row.DatasetIDs); len(got) != 0 {
		t.Fatalf("expected empty dataset_ids in db, got %v", got)
	}
}

func TestCreateEvalSetRejectsDuplicateNameForSameOwner(t *testing.T) {
	db := newEvalSetTestDB(t)
	existing := seedEvalSet(t, db, "eval_set_existing", "owner_1", "", "", time.Now().UTC())
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", existing.ID).Update("name", "wbc_test").Error; err != nil {
		t.Fatalf("update existing name: %v", err)
	}

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets", `{"name":" wbc_test ","dataset_ids":["dataset_1"]}`, "owner_1")
	CreateEvalSet(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dataset name already exists") {
		t.Fatalf("expected duplicate name message, got %s", rec.Body.String())
	}
}

func TestUpdateEvalSetRejectsDuplicateNameForSameOwner(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	existing := seedEvalSet(t, db, "eval_set_existing", "owner_1", "", "", now)
	target := seedEvalSet(t, db, "eval_set_target", "owner_1", "", "", now)
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", existing.ID).Update("name", "wbc_test").Error; err != nil {
		t.Fatalf("update existing name: %v", err)
	}

	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_target", `{"name":"WBC_TEST"}`, "owner_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": target.ID})
	UpdateEvalSet(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dataset name already exists") {
		t.Fatalf("expected duplicate name message, got %s", rec.Body.String())
	}
}

func TestListEvalSetsOnlyReturnsAccessibleRows(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	seedEvalSet(t, db, "eval_set_owned", "user_1", "", "", now)
	seedEvalSet(t, db, "eval_set_group", "user_2", "group_1", "", now.Add(-time.Minute))
	seedEvalSet(t, db, "eval_set_acl", "user_2", "", "", now.Add(-2*time.Minute))
	seedEvalSet(t, db, "eval_set_hidden", "user_2", "", "", now.Add(-3*time.Minute))

	if err := db.Create(&orm.UserGroupModel{UserID: "user_1", GroupID: "group_1"}).Error; err != nil {
		t.Fatalf("seed user group: %v", err)
	}
	if id := acl.GetStore().AddACL(acl.ResourceTypeEvalSet, "eval_set_acl", acl.GranteeUser, "user_1", acl.PermissionEvalSetRead, "user_2", nil); id == 0 {
		t.Fatalf("expected acl row")
	}

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets?page=1&page_size=10", "", "user_1")
	ListEvalSets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ListEvalSetsResponse](t, rec)
	got := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		got = append(got, item.ID)
	}
	sort.Strings(got)
	want := []string{"eval_set_acl", "eval_set_group", "eval_set_owned"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected ids %v, got %v", want, got)
	}
	if resp.Total != int64(len(want)) {
		t.Fatalf("expected total %d, got %d", len(want), resp.Total)
	}
}

func TestListEvalSetsFiltersByAnyDatasetID(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	seedEvalSet(t, db, "eval_set_match_a", "user_1", "", "dataset_a,dataset_b", now)
	seedEvalSet(t, db, "eval_set_match_b", "user_1", "", "dataset_c,dataset_d", now.Add(-time.Minute))
	seedEvalSet(t, db, "eval_set_skip", "user_1", "", "dataset_e", now.Add(-2*time.Minute))

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets?dataset_ids=dataset_b,dataset_d&page=1&page_size=10", "", "user_1")
	ListEvalSets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ListEvalSetsResponse](t, rec)
	got := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		got = append(got, item.ID)
	}
	sort.Strings(got)
	want := []string{"eval_set_match_a", "eval_set_match_b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected ids %v, got %v", want, got)
	}
	if resp.Total != int64(len(want)) {
		t.Fatalf("expected total %d, got %d", len(want), resp.Total)
	}
}

func TestListEvalSetsKeywordIsCaseInsensitive(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	match := seedEvalSet(t, db, "eval_set_qwen", "user_1", "", "", now)
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", match.ID).Updates(map[string]any{
		"name":        "Qwen Cases",
		"description": "Model eval set",
	}).Error; err != nil {
		t.Fatalf("update match eval set: %v", err)
	}
	skip := seedEvalSet(t, db, "eval_set_other", "user_1", "", "", now.Add(-time.Minute))
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", skip.ID).Updates(map[string]any{
		"name":        "Other Cases",
		"description": "No matching keyword",
	}).Error; err != nil {
		t.Fatalf("update skip eval set: %v", err)
	}

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets?keyword=qwen&page=1&page_size=10", "", "user_1")
	ListEvalSets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ListEvalSetsResponse](t, rec)
	if resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0].ID != "eval_set_qwen" {
		t.Fatalf("expected only qwen eval set, got %#v", resp)
	}
}

func TestListEvalSetsKeywordMatchesBackslash(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	match := seedEvalSet(t, db, "eval_set_backslash", "user_1", "", "", now)
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", match.ID).Updates(map[string]any{
		"name":        `Windows \ Cases`,
		"description": "Model eval set",
	}).Error; err != nil {
		t.Fatalf("update match eval set: %v", err)
	}
	seedEvalSet(t, db, "eval_set_other", "user_1", "", "", now.Add(-time.Minute))

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets?keyword=%5C&page=1&page_size=10", "", "user_1")
	ListEvalSets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ListEvalSetsResponse](t, rec)
	if resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0].ID != "eval_set_backslash" {
		t.Fatalf("expected only backslash eval set, got %#v", resp)
	}
}

func TestGetEvalSetForbiddenWithoutPermission(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_private", "owner_1", "", "", time.Now().UTC())

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_private", "", "user_2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_private"})
	GetEvalSet(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateEvalSetMetadata(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_update", "user_1", "", "dataset_old", time.Now().UTC())

	body := `{"name":"new name","description":"new desc","dataset_ids":["dataset_new","dataset_extra"]}`
	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_update", body, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_update"})
	UpdateEvalSet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[EvalSetResponse](t, rec)
	if resp.Name != "new name" || resp.Description != "new desc" || strings.Join(resp.DatasetIDs, ",") != "dataset_new,dataset_extra" {
		t.Fatalf("unexpected response: %#v", resp)
	}

	var row orm.EvalSet
	if err := db.First(&row, "id = ?", "eval_set_update").Error; err != nil {
		t.Fatalf("query updated eval set: %v", err)
	}
	if row.Name != "new name" || row.Description != "new desc" || strings.Join(parseDatasetIDsJSON(row.DatasetIDs), ",") != "dataset_new,dataset_extra" {
		t.Fatalf("unexpected row: %#v", row)
	}
}

func TestUpdateEvalSetAllowsClearingDatasetIDs(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_clear_datasets", "user_1", "", "dataset_old", time.Now().UTC())

	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_clear_datasets", `{"dataset_ids":[]}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_clear_datasets"})
	UpdateEvalSet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[EvalSetResponse](t, rec)
	if len(resp.DatasetIDs) != 0 || len(resp.DatasetNames) != 0 {
		t.Fatalf("expected cleared dataset ids, got %#v", resp)
	}

	var row orm.EvalSet
	if err := db.First(&row, "id = ?", "eval_set_clear_datasets").Error; err != nil {
		t.Fatalf("query updated eval set: %v", err)
	}
	if got := parseDatasetIDsJSON(row.DatasetIDs); len(got) != 0 {
		t.Fatalf("expected empty dataset_ids in db, got %v", got)
	}
}

func TestUpdateEvalSetRejectsEmptyName(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_empty_name", "user_1", "", "", time.Now().UTC())

	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_empty_name", `{"name":"   "}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_empty_name"})
	UpdateEvalSet(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteEvalSetPhysicallyDeletesAndRemovesFromList(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	seedEvalSet(t, db, "eval_set_delete", "user_1", "", "", now)
	if err := db.Model(&orm.EvalSetShard{}).Where("id = ?", DefaultShardID).Updates(map[string]any{
		"actual_rows":     1,
		"estimated_bytes": int64(42),
	}).Error; err != nil {
		t.Fatalf("update shard counters: %v", err)
	}
	if err := db.Create(&orm.EvalSetItem{
		ShardID:        DefaultShardID,
		ID:             "item_1",
		EvalSetID:      "eval_set_delete",
		Question:       "question",
		GroundTruth:    "answer",
		QuestionType:   "1",
		EstimatedBytes: 42,
		Source:         SourceManual,
		CreateUserID:   "user_1",
		CreateUserName: "user_1 name",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if id := acl.GetStore().AddACL(acl.ResourceTypeEvalSet, "eval_set_delete", acl.GranteeUser, "user_1", acl.PermissionEvalSetRead, "user_1", nil); id == 0 {
		t.Fatalf("expected acl row")
	}

	rec, req := requestWithUser(http.MethodDelete, "/api/core/eval-sets/eval_set_delete", "", "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_delete"})
	DeleteEvalSet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[DeleteEvalSetResponse](t, rec)
	if !resp.Deleted {
		t.Fatalf("expected deleted true")
	}

	var evalSetCount int64
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", "eval_set_delete").Count(&evalSetCount).Error; err != nil {
		t.Fatalf("count eval sets: %v", err)
	}
	if evalSetCount != 0 {
		t.Fatalf("expected eval set deleted, count=%d", evalSetCount)
	}
	var itemCount int64
	if err := db.Model(&orm.EvalSetItem{}).Where("eval_set_id = ?", "eval_set_delete").Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("expected items deleted, count=%d", itemCount)
	}

	rec, req = requestWithUser(http.MethodGet, "/api/core/eval-sets", "", "user_1")
	ListEvalSets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeOKData[ListEvalSetsResponse](t, rec)
	if len(list.Items) != 0 {
		t.Fatalf("expected deleted eval set absent from list, got %#v", list.Items)
	}
}

func permissionsFromACL(rows []orm.ACLModel) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Permission)
	}
	sort.Strings(out)
	return out
}
