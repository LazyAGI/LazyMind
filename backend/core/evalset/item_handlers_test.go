package evalset

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func seedEvalSetItem(t *testing.T, db *orm.DB, item orm.EvalSetItem) orm.EvalSetItem {
	t.Helper()
	now := time.Now().UTC()
	if item.ShardID == "" {
		item.ShardID = DefaultShardID
	}
	if item.CaseID == "" {
		item.CaseID = item.ID
	}
	if item.Question == "" {
		item.Question = "question " + item.ID
	}
	if item.GroundTruth == "" {
		item.GroundTruth = "answer " + item.ID
	}
	if item.QuestionType == "" {
		item.QuestionType = "type"
	}
	if item.Source == "" {
		item.Source = SourceManual
	}
	if item.CreateUserID == "" {
		item.CreateUserID = "owner_1"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	if item.EstimatedBytes == 0 {
		item.EstimatedBytes = estimateEvalSetItemBytes(&item)
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("seed eval set item %s: %v", item.ID, err)
	}
	return item
}

func TestCreateEvalSetItemManualSource(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_create", "user_1", "", "", time.Now().UTC())

	body := `{"case_id":"case_001","question":"How?","ground_truth":"Like this","question_type":"操作问答","reference_context":"  line 1\r\nline 2  ","source":"upload"}`
	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_items_create/items", body, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_create"})
	CreateEvalSetItem(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rawResponse := rec.Body.String()
	resp := decodeOKData[EvalSetItemResponse](t, rec)
	if !strings.HasPrefix(resp.ID, "eval_item_") {
		t.Fatalf("expected eval_item_ id, got %q", resp.ID)
	}
	if resp.Source != SourceManual {
		t.Fatalf("expected source manual, got %q", resp.Source)
	}
	if resp.EvalSetID != "eval_set_items_create" || resp.ShardID != DefaultShardID {
		t.Fatalf("unexpected eval set or shard: %#v", resp)
	}
	if resp.ReferenceContext != "line 1\r\nline 2" {
		t.Fatalf("expected frontend reference context to be preserved after trim, got %q", resp.ReferenceContext)
	}
	var responseFields map[string]any
	if err := json.Unmarshal([]byte(rawResponse), &responseFields); err != nil {
		t.Fatalf("decode response fields: %v", err)
	}
	data, ok := responseFields["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected response data object, got %#v", responseFields["data"])
	}
	if _, ok := data["algorithm_reference_context"]; ok {
		t.Fatalf("algorithm_reference_context must not be returned to frontend: %#v", data)
	}
	var createdItem orm.EvalSetItem
	if err := db.First(&createdItem, "id = ?", resp.ID).Error; err != nil {
		t.Fatalf("query created item: %v", err)
	}
	if createdItem.AlgorithmReferenceContext != "line 1\nline 2" {
		t.Fatalf("expected algorithm reference context to be derived, got %q", createdItem.AlgorithmReferenceContext)
	}

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", "eval_set_items_create").Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 1 {
		t.Fatalf("expected item_count 1, got %d", evalSet.ItemCount)
	}
	var shard orm.EvalSetShard
	if err := db.First(&shard, "id = ?", DefaultShardID).Error; err != nil {
		t.Fatalf("query shard: %v", err)
	}
	if shard.ActualRows != 1 || shard.EstimatedBytes <= 0 {
		t.Fatalf("expected shard counters updated, got rows=%d bytes=%d", shard.ActualRows, shard.EstimatedBytes)
	}
}

func TestCreateEvalSetItemRequiresQuestion(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_missing_question", "user_1", "", "", time.Now().UTC())

	body := `{"ground_truth":"answer","question_type":"type"}`
	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_items_missing_question/items", body, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_missing_question"})
	CreateEvalSetItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListEvalSetItemsFiltersBySourceAndQuestionType(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_filter", "user_1", "", "", time.Now().UTC())
	seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_manual_1", EvalSetID: "eval_set_items_filter", Source: SourceManual, QuestionType: "A"})
	seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_upload_1", EvalSetID: "eval_set_items_filter", Source: SourceUpload, QuestionType: "A"})
	seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_manual_2", EvalSetID: "eval_set_items_filter", Source: SourceManual, QuestionType: "B"})

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_items_filter/items?source=manual", "", "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_filter"})
	ListEvalSetItems(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	bySource := decodeOKData[ListEvalSetItemsResponse](t, rec)
	if bySource.Total != 2 || len(bySource.Items) != 2 {
		t.Fatalf("expected 2 manual items, got total=%d items=%d", bySource.Total, len(bySource.Items))
	}
	for _, item := range bySource.Items {
		if item.Source != SourceManual {
			t.Fatalf("expected only manual source, got %#v", bySource.Items)
		}
	}

	rec, req = requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_items_filter/items?question_type=A", "", "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_filter"})
	ListEvalSetItems(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	byType := decodeOKData[ListEvalSetItemsResponse](t, rec)
	if byType.Total != 2 || len(byType.Items) != 2 {
		t.Fatalf("expected 2 type A items, got total=%d items=%d", byType.Total, len(byType.Items))
	}
	for _, item := range byType.Items {
		if item.QuestionType != "A" {
			t.Fatalf("expected only type A, got %#v", byType.Items)
		}
	}
}

func TestListEvalSetItemsMarksKnowledgeBaseReferenceDocAndChunkSelection(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	seedEvalSet(t, db, "eval_set_items_reference", "user_1", "", "kb_1", now)
	if err := db.Create(&orm.Document{
		ID:          "doc_kb",
		DatasetID:   "kb_1",
		DisplayName: "knowledge doc",
		BaseModel: orm.BaseModel{
			CreateUserID:   "user_1",
			CreateUserName: "user_1 name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("seed document: %v", err)
	}
	seedEvalSetItem(t, db, orm.EvalSetItem{
		ID:                "eval_item_with_chunk",
		EvalSetID:         "eval_set_items_reference",
		ReferenceDocIDs:   "doc_kb",
		ReferenceChunkIDs: "chunk_1",
	})
	seedEvalSetItem(t, db, orm.EvalSetItem{
		ID:              "eval_item_without_chunk",
		EvalSetID:       "eval_set_items_reference",
		ReferenceDocIDs: "doc_kb",
	})
	seedEvalSetItem(t, db, orm.EvalSetItem{
		ID:              "eval_item_external_doc",
		EvalSetID:       "eval_set_items_reference",
		ReferenceDocIDs: "doc_external",
	})

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_items_reference/items", "", "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_reference"})
	ListEvalSetItems(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ListEvalSetItemsResponse](t, rec)
	itemsByID := make(map[string]EvalSetItemResponse, len(resp.Items))
	for _, item := range resp.Items {
		itemsByID[item.ID] = item
	}

	withChunk := itemsByID["eval_item_with_chunk"]
	if !withChunk.ReferenceDocFromKnowledgeBase || !withChunk.ReferenceChunkSelected {
		t.Fatalf("expected kb doc with selected chunk flags, got %#v", withChunk)
	}
	withoutChunk := itemsByID["eval_item_without_chunk"]
	if !withoutChunk.ReferenceDocFromKnowledgeBase || withoutChunk.ReferenceChunkSelected {
		t.Fatalf("expected kb doc without selected chunk flags, got %#v", withoutChunk)
	}
	externalDoc := itemsByID["eval_item_external_doc"]
	if externalDoc.ReferenceDocFromKnowledgeBase || externalDoc.ReferenceChunkSelected {
		t.Fatalf("expected external doc flags false, got %#v", externalDoc)
	}
}

func TestUpdateEvalSetItemKeepsUploadSource(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_update", "user_1", "", "", time.Now().UTC())
	seedEvalSetItem(t, db, orm.EvalSetItem{
		ID:           "eval_item_upload",
		EvalSetID:    "eval_set_items_update",
		Source:       SourceUpload,
		QuestionType: "old",
	})

	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_items_update/items/eval_item_upload", `{"question_type":"new","ground_truth":"updated"}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_update", "item_id": "eval_item_upload"})
	UpdateEvalSetItem(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[EvalSetItemResponse](t, rec)
	if resp.Source != SourceUpload {
		t.Fatalf("expected source upload to remain, got %q", resp.Source)
	}
	if resp.QuestionType != "new" || resp.GroundTruth != "updated" {
		t.Fatalf("unexpected updated item: %#v", resp)
	}
}

func TestUpdateEvalSetItemDerivesAlgorithmReferenceContext(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_update_context", "user_1", "", "", time.Now().UTC())
	seedEvalSetItem(t, db, orm.EvalSetItem{
		ID:                        "eval_item_context",
		EvalSetID:                 "eval_set_items_update_context",
		ReferenceContext:          "old frontend",
		AlgorithmReferenceContext: "old algorithm",
	})

	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_items_update_context/items/eval_item_context", `{"reference_context":"  new\r\ncontext  "}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_update_context", "item_id": "eval_item_context"})
	UpdateEvalSetItem(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[EvalSetItemResponse](t, rec)
	if resp.ReferenceContext != "new\r\ncontext" {
		t.Fatalf("expected frontend reference context in response, got %q", resp.ReferenceContext)
	}
	var item orm.EvalSetItem
	if err := db.First(&item, "id = ?", "eval_item_context").Error; err != nil {
		t.Fatalf("query updated item: %v", err)
	}
	if item.AlgorithmReferenceContext != "new\ncontext" {
		t.Fatalf("expected derived algorithm reference context, got %q", item.AlgorithmReferenceContext)
	}
}

func TestUpdateEvalSetItemDerivesAlgorithmReferenceContextFromStructuredParts(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_structured_context", "user_1", "", "", time.Now().UTC())
	seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_structured_context", EvalSetID: "eval_set_items_structured_context"})

	payload := `{"type":"reference_context","version":1,"parts":[{"type":"chunk","content":"片段一内容"},{"type":"text","content":"用户补充内容"},{"type":"chunk","content":"片段二内容"}]}`
	bodyBytes, err := json.Marshal(map[string]string{"reference_context": payload})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_items_structured_context/items/eval_item_structured_context", string(bodyBytes), "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_structured_context", "item_id": "eval_item_structured_context"})
	UpdateEvalSetItem(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var item orm.EvalSetItem
	if err := db.First(&item, "id = ?", "eval_item_structured_context").Error; err != nil {
		t.Fatalf("query updated item: %v", err)
	}
	if item.ReferenceContext != payload {
		t.Fatalf("expected frontend structured context to be stored, got %q", item.ReferenceContext)
	}
	wantAlgorithmContext := "片段一内容\n\n用户补充内容\n\n片段二内容"
	if item.AlgorithmReferenceContext != wantAlgorithmContext {
		t.Fatalf("expected derived algorithm reference context %q, got %q", wantAlgorithmContext, item.AlgorithmReferenceContext)
	}
}

func TestUpdateEvalSetItemRejectsEmptyGroundTruth(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_empty_ground_truth", "user_1", "", "", time.Now().UTC())
	seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_patch_empty", EvalSetID: "eval_set_items_empty_ground_truth"})

	rec, req := requestWithUser(http.MethodPatch, "/api/core/eval-sets/eval_set_items_empty_ground_truth/items/eval_item_patch_empty", `{"ground_truth":"   "}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_empty_ground_truth", "item_id": "eval_item_patch_empty"})
	UpdateEvalSetItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteEvalSetItemPhysicallyDeletesAndUpdatesCounters(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	seedEvalSet(t, db, "eval_set_items_delete", "user_1", "", "", now)
	item := seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_delete", EvalSetID: "eval_set_items_delete"})
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", "eval_set_items_delete").Update("item_count", 1).Error; err != nil {
		t.Fatalf("update eval set count: %v", err)
	}
	if err := db.Model(&orm.EvalSetShard{}).Where("id = ?", DefaultShardID).Updates(map[string]any{
		"actual_rows":     1,
		"estimated_bytes": item.EstimatedBytes,
	}).Error; err != nil {
		t.Fatalf("update shard counters: %v", err)
	}

	rec, req := requestWithUser(http.MethodDelete, "/api/core/eval-sets/eval_set_items_delete/items/eval_item_delete", "", "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_delete", "item_id": "eval_item_delete"})
	DeleteEvalSetItem(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[DeleteEvalSetItemResponse](t, rec)
	if !resp.Deleted {
		t.Fatalf("expected deleted true")
	}

	rec, req = requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_items_delete/items", "", "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_delete"})
	ListEvalSetItems(rec, req)
	list := decodeOKData[ListEvalSetItemsResponse](t, rec)
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("expected deleted item absent, got %#v", list)
	}

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", "eval_set_items_delete").Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 0 {
		t.Fatalf("expected item_count 0, got %d", evalSet.ItemCount)
	}
	var shard orm.EvalSetShard
	if err := db.First(&shard, "id = ?", DefaultShardID).Error; err != nil {
		t.Fatalf("query shard: %v", err)
	}
	if shard.ActualRows != 0 || shard.EstimatedBytes != 0 {
		t.Fatalf("expected shard counters zero, got rows=%d bytes=%d", shard.ActualRows, shard.EstimatedBytes)
	}
}

func TestBatchDeleteEvalSetItemsRejectsEmptySelection(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_batch_empty", "user_1", "", "", time.Now().UTC())

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_items_batch_empty/items:batchDelete", `{"item_ids":[]}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_batch_empty"})
	BatchDeleteEvalSetItems(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var envelope common.APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Message != "请先选择样本" {
		t.Fatalf("expected message 请先选择样本, got %q", envelope.Message)
	}
}

func TestBatchDeleteEvalSetItemsDoesNotDeleteOtherEvalSetItems(t *testing.T) {
	db := newEvalSetTestDB(t)
	now := time.Now().UTC()
	seedEvalSet(t, db, "eval_set_items_batch_a", "user_1", "", "", now)
	seedEvalSet(t, db, "eval_set_items_batch_b", "user_1", "", "", now)
	itemA := seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_batch_a", EvalSetID: "eval_set_items_batch_a"})
	itemB := seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_batch_b", EvalSetID: "eval_set_items_batch_b"})
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", "eval_set_items_batch_a").Update("item_count", 1).Error; err != nil {
		t.Fatalf("update eval set a count: %v", err)
	}
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", "eval_set_items_batch_b").Update("item_count", 1).Error; err != nil {
		t.Fatalf("update eval set b count: %v", err)
	}
	if err := db.Model(&orm.EvalSetShard{}).Where("id = ?", DefaultShardID).Updates(map[string]any{
		"actual_rows":     2,
		"estimated_bytes": itemA.EstimatedBytes + itemB.EstimatedBytes,
	}).Error; err != nil {
		t.Fatalf("update shard counters: %v", err)
	}

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_items_batch_a/items:batchDelete", `{"item_ids":["eval_item_batch_a","eval_item_batch_b"]}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_batch_a"})
	BatchDeleteEvalSetItems(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[BatchDeleteEvalSetItemsResponse](t, rec)
	if resp.DeletedCount != 1 {
		t.Fatalf("expected deleted_count 1, got %d", resp.DeletedCount)
	}

	var countA, countB int64
	if err := db.Model(&orm.EvalSetItem{}).Where("shard_id = ? AND eval_set_id = ?", DefaultShardID, "eval_set_items_batch_a").Count(&countA).Error; err != nil {
		t.Fatalf("count eval set a items: %v", err)
	}
	if err := db.Model(&orm.EvalSetItem{}).Where("shard_id = ? AND eval_set_id = ?", DefaultShardID, "eval_set_items_batch_b").Count(&countB).Error; err != nil {
		t.Fatalf("count eval set b items: %v", err)
	}
	if countA != 0 || countB != 1 {
		t.Fatalf("expected only eval set a item deleted, got countA=%d countB=%d", countA, countB)
	}

	var evalSetA, evalSetB orm.EvalSet
	if err := db.First(&evalSetA, "id = ?", "eval_set_items_batch_a").Error; err != nil {
		t.Fatalf("query eval set a: %v", err)
	}
	if err := db.First(&evalSetB, "id = ?", "eval_set_items_batch_b").Error; err != nil {
		t.Fatalf("query eval set b: %v", err)
	}
	if evalSetA.ItemCount != 0 || evalSetB.ItemCount != 1 {
		t.Fatalf("unexpected item counts: a=%d b=%d", evalSetA.ItemCount, evalSetB.ItemCount)
	}
}

func TestListEvalSetItemsForbiddenWithoutPermission(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_items_private", "owner_1", "", "", time.Now().UTC())

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_items_private/items", "", "user_2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_items_private"})
	ListEvalSetItems(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
