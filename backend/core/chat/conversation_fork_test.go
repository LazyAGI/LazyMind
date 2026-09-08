package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func forkFixture(t *testing.T, count int) (*gorm.DB, orm.Conversation, []orm.ChatHistory, forkCreateRequest) {
	t.Helper()
	db := newPromptTestDB(t).DB
	seedAvailableChatModel(t, db, "u1", "provider-fork", "group-fork", "model-fork", "Test", "Test", "A", "llm", true, "fake-test-key")
	c := sidechatTestConversation(t, db, "fork-source", "u1", "Source")
	items := make([]orm.ChatHistory, 0, count)
	for i := 1; i <= count; i++ {
		h := sidechatTestHistory(t, db, fmt.Sprintf("source-%04d", i), c.ID, i, fmt.Sprintf("q%d", i), fmt.Sprintf("a%d", i), "completed")
		h.Ext = mergeConversationConfigSnapshot(nil, map[string]any{chatModelRouteBodyKey: &chatModelRoute{Mode: "fixed", ModelID: "model-fork", ModelName: "A"}, "thinking_depth": "high", "enable_workflow": false, "enable_subagent": false, "mode": "auto", "reasoning": true})
		if err := db.Model(&h).Update("ext", h.Ext).Error; err != nil {
			t.Fatal(err)
		}
		items = append(items, h)
	}
	revision, err := forkPrefixRevision(items)
	if err != nil {
		t.Fatal(err)
	}
	return db, c, items, forkCreateRequest{SourceHistoryID: items[len(items)-1].ID, ExpectedPrefixRevision: revision}
}

func TestForkCreatesCompleteIndependentPrefixAndReplaysAfterSourceDeletion(t *testing.T) {
	db, source, items, request := forkFixture(t, 105)
	ctx := context.Background()
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	sidechatTestHistory(t, db, "future", source.ID, 106, "future input", "", "generating")
	result, err := createConversationFork(ctx, db, caller, source.ID, "key-one", request)
	if err != nil {
		t.Fatal(err)
	}
	id := result.Conversation["conversation_id"].(string)
	var copied []orm.ChatHistory
	if err := db.Where("conversation_id = ?", id).Order("seq").Find(&copied).Error; err != nil {
		t.Fatal(err)
	}
	if len(copied) != 105 || copied[104].Result != "a105" || copied[0].ID == items[0].ID {
		t.Fatal("prefix truncated or identities shared")
	}
	var branch orm.Conversation
	if err := db.Where("id = ?", id).Take(&branch).Error; err != nil {
		t.Fatal(err)
	}
	if branch.ParentConversationID != nil || branch.ThinkingDepth != "high" || branch.ChatTimes != 105 || branch.ChatModelID == nil || *branch.ChatModelID != "model-fork" {
		t.Fatalf("incorrect branch: %#v", branch)
	}
	if err := archiveConversation(ctx, db, source.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	replay, err := createConversationFork(ctx, db, caller, source.ID, "key-one", request)
	if err != nil || !replay.Replayed || replay.Conversation["conversation_id"] != id {
		t.Fatalf("replay after deletion: %#v %v", replay, err)
	}
	if err := db.Where("id = ? AND deleted_at IS NULL", id).Take(&branch).Error; err != nil {
		t.Fatal("branch deleted with source")
	}
	if err := db.Where("id = ?", id).Delete(&orm.Conversation{}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = createConversationFork(ctx, db, caller, source.ID, "key-one", request)
	var problem *forkError
	if !errors.As(err, &problem) || problem.Code != "FORK_RESULT_UNAVAILABLE" {
		t.Fatalf("deleted result recreated: %v", err)
	}
}

func TestForkConcurrentIdempotencyAndIndependentNewOperation(t *testing.T) {
	db, c, _, request := forkFixture(t, 3)
	const workers = 5
	results := make(chan *forkResult, workers)
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "concurrent-key", request)
			if err != nil {
				failures <- err
			} else {
				results <- result
			}
		}()
	}
	wg.Wait()
	close(failures)
	close(results)
	for err := range failures {
		t.Error(err)
	}
	id := ""
	for result := range results {
		next := result.Conversation["conversation_id"].(string)
		if id != "" && id != next {
			t.Fatal("duplicate fork")
		}
		id = next
	}
	var count int64
	if err := db.Model(&orm.ConversationForkRequest{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("receipts=%d err=%v", count, err)
	}
	newResult, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "another-operation", request)
	if err != nil || newResult.Conversation["conversation_id"] == id {
		t.Fatalf("new operation did not create: %v", err)
	}
}

func TestForkRollbackAndSourceChanges(t *testing.T) {
	db, c, items, request := forkFixture(t, 2)
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	ctx := context.Background()
	callback := "test:fork_history_failure"
	if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "chat_histories" {
			tx.AddError(errors.New("injected write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := createConversationFork(ctx, db, caller, c.ID, "rollback-key", request)
	if err == nil {
		t.Fatal("failure injection did not fail")
	}
	if err := db.Callback().Create().Remove(callback); err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&orm.ConversationForkOrigin{}, &orm.ConversationForkRequest{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("partial write: %T count %d err %v", model, count, err)
		}
	}
	var count int64
	db.Model(&orm.Conversation{}).Count(&count)
	if count != 1 {
		t.Fatal("empty conversation leaked")
	}
	if err := db.Model(&items[0]).Update("result", "changed").Error; err != nil {
		t.Fatal(err)
	}
	_, err = createConversationFork(ctx, db, caller, c.ID, "rollback-key", request)
	var problem *forkError
	if !errors.As(err, &problem) || problem.Code != "SOURCE_CHANGED" {
		t.Fatalf("accepted changed prefix: %v", err)
	}
	_, err = createConversationFork(ctx, db, doc.DatasetCatalogCaller{UserID: "foreign"}, c.ID, "foreign-key", request)
	if !errors.As(err, &problem) || problem.Code != "SOURCE_UNAVAILABLE" {
		t.Fatalf("unauthorized fork: %v", err)
	}
}

func TestForkLegacyRequiresEveryMissingFieldConfirmation(t *testing.T) {
	db, c, items, _ := forkFixture(t, 1)
	items[0].Ext = json.RawMessage(`{"model_route":{"mode":"fixed","model_id":"model-fork"}}`)
	if err := db.Model(&items[0]).Update("ext", items[0].Ext).Error; err != nil {
		t.Fatal(err)
	}
	p, err := buildForkPreview(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c, items)
	if err != nil {
		t.Fatal(err)
	}
	request := forkCreateRequest{SourceHistoryID: items[0].ID, ExpectedPrefixRevision: p.PrefixRevision}
	_, err = createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "legacy", request)
	var problem *forkError
	if !errors.As(err, &problem) || problem.Code != "CONFIG_CONFIRMATION_REQUIRED" {
		t.Fatalf("silent defaults: %v", err)
	}
	for _, issue := range p.ConfigIssues {
		request.ConfirmedFields = append(request.ConfirmedFields, issue.Field)
		if request.ConfirmedValues == nil {
			request.ConfirmedValues = map[string]any{}
		}
		request.ConfirmedValues[issue.Field] = issue.SuggestedValue
	}
	if _, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "legacy", request); err != nil {
		t.Fatal(err)
	}
}
