package main

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/resourceupdate"
	"lazymind/core/state"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type coreBootstrapParticipant interface {
	BeginUpdateBootstrap(transactionID, owner string) error
	CompleteUpdateBootstrap(transactionID, owner string) error
}

type resourceUpdateAssembler interface {
	startResourceUpdates(context.Context, *gorm.DB, state.Store, resourceupdate.Config, bool) []<-chan struct{}
}

func TestDesktopUpdateBootstrapProductionResourceAssemblyIncludesIdleOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle-only", true: "all-loops"}[enabled], func(t *testing.T) {
			db := updateActivityDB(t)
			if err := db.AutoMigrate(&orm.ConversationIdleEvent{}, &orm.Conversation{}, &orm.MemoryCurrentEntry{}, &orm.SkillReviewSchedulerState{}, &orm.SkillV2Skill{}, &orm.SkillV2Blob{}, &orm.SkillV2Revision{}, &orm.SkillV2RevisionEntry{}, &orm.SkillV2Draft{}, &orm.SkillV2DraftEntry{}, &orm.SkillDraftReviewSession{}, &orm.SkillDraftReviewActionBatch{}, &orm.SkillDraftReviewActionItem{}, &orm.SkillSearchIndex{}); err != nil {
				t.Fatal(err)
			}
			store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			control := &desktopUpdateControl{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
			assembly, ok := any(control).(resourceUpdateAssembler)
			if !ok {
				t.Fatal("Core has no update-aware resource runtime assembly")
			}
			participant, ok := any(control).(coreBootstrapParticipant)
			if !ok {
				t.Fatal("Core has no initial bootstrap")
			}
			tx := strings.Repeat("a", 32)
			if err := participant.BeginUpdateBootstrap(tx, "fixture-owner"); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			event := orm.ConversationIdleEvent{ID: "fixture-event", EventID: "fixture-event", ConversationID: "fixture-conversation", UserID: "fixture-owner", LastHistoryID: "fixture-history", Status: "waiting", DueAt: now.Add(-time.Minute), LastActivityAt: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&event).Error; err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{}, 1)
			old := resourceupdate.DirectoryIdleHandler
			resourceupdate.DirectoryIdleHandler = func(context.Context, *gorm.DB, orm.ConversationIdleEvent) error {
				select {
				case entered <- struct{}{}:
				default:
				}
				return nil
			}
			defer func() { resourceupdate.DirectoryIdleHandler = old }()
			ctx, cancel := context.WithCancel(context.Background())
			done := assembly.startResourceUpdates(ctx, db, store, resourceupdate.Config{WorkerInterval: time.Millisecond, SchedulerTickInterval: time.Millisecond, ScannerInterval: time.Millisecond, ConversationIdleFallbackScanInterval: time.Millisecond}, enabled)
			defer func() {
				cancel()
				for _, channel := range done {
					select {
					case <-channel:
					case <-time.After(3 * time.Second):
						t.Error("resource assembly did not close")
					}
				}
			}()
			if len(done) == 0 {
				t.Fatal("idle-only assembly silently omitted background owner")
			}
			select {
			case <-entered:
				t.Fatal("actual StartIdle escaped bootstrap before its first callback")
			case <-time.After(30 * time.Millisecond):
			}
			if err := participant.CompleteUpdateBootstrap(tx, "fixture-owner"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("production assembly lost deferred idle work")
			}
		})
	}
	// The behavioral test above uses the same assembler; prove ordinary run()
	// actually invokes it rather than leaving a correct helper unused.
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wired := false
	var bootstrapAt, workAt token.Pos
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "BeginUpdateBootstrap" {
				bootstrapAt = call.Pos()
			}
			if ok && selector.Sel.Name == "startResourceUpdates" {
				wired = true
				workAt = call.Pos()
			}
			return true
		})
	}
	if !wired {
		t.Fatal("ordinary Core run bypasses update-aware resource assembly")
	}
	if bootstrapAt == token.NoPos || bootstrapAt >= workAt {
		t.Fatal("Core must construct and seal its production control before starting any resource runtime")
	}
}

func TestDesktopUpdateBootstrapCoreRemainsClosedUntilExactRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		control := &desktopUpdateControl{
			token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil },
		}
		participant, ok := any(control).(coreBootstrapParticipant)
		if !ok {
			t.Fatal("Core has no non-expiring target startup admission")
		}
		tx := strings.Repeat("a", 32)
		if err := participant.BeginUpdateBootstrap(tx, "fixture-owner"); err != nil {
			t.Fatal(err)
		}
		var handled int
		handler := control.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handled++
			w.WriteHeader(http.StatusNoContent)
		}))
		assertBlocked := func() {
			t.Helper()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/fixture-task", nil))
			if response.Code != http.StatusServiceUnavailable || handled != 0 {
				t.Fatal("Core reopened user work before commit")
			}
		}
		assertBlocked()
		updateControlRequest(t, handler, "pause", 1)
		time.Sleep(3 * time.Minute)
		updateControlRequest(t, handler, "resume", 1)
		assertBlocked()
		var state map[string]any
		if json.Unmarshal(updateControlRequest(t, handler, "status", 0).Body.Bytes(), &state) != nil {
			t.Fatal("unreadable Core bootstrap proof")
		}
		proof, _ := state["bootstrap"].(map[string]any)
		if proof["transactionId"] != tx || proof["owner"] != "fixture-owner" || proof["sealed"] != true {
			t.Fatalf("Core did not prove its own fence: %+v", state)
		}
		if err := participant.CompleteUpdateBootstrap(tx, "other-owner"); err == nil {
			t.Fatal("wrong owner released Core")
		}
		assertBlocked()
		if err := participant.CompleteUpdateBootstrap(tx, "fixture-owner"); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/fixture-task", nil))
		if response.Code != http.StatusNoContent || handled != 1 {
			t.Fatal("committed Core did not reopen")
		}
		if err := participant.CompleteUpdateBootstrap(tx, "fixture-owner"); err != nil {
			t.Fatal("same release must be idempotent")
		}
	})
}
