package knowledgeruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/doc"
	"lazymind/core/state"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestConfirmationBindsExecutionArgumentsAndSnapshot(t *testing.T) {
	st, e := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	active := true
	snapshot := "v1"
	rt, e := New(st, func(_ context.Context, x Execution) (Identity, error) {
		if !active || x.UserID != "u" {
			return Identity{}, errors.New("inactive")
		}
		return Identity{Principal: capability.Principal{UserID: "u", TenantID: "t"}, AllowedTools: []string{"knowledge.resource.delete", "knowledge.create"}}, nil
	}, func(context.Context, Identity, string, json.RawMessage) (doc.ResourceSnapshot, error) {
		return doc.ResourceSnapshot{Digest: snapshot}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	req := PrepareRequest{Execution: Execution{UserID: "u", RunID: "run"}, CallID: "call", WireName: "knowledge.resource.delete", Arguments: json.RawMessage(`{"targets":[{"kind":"document","knowledge_base_id":"kb","document_id":"d"}]}`)}
	p, e := rt.Prepare(ctx, req)
	if e != nil || p.Status != "pending" {
		t.Fatalf("%+v %v", p, e)
	}
	req.OperationKey = p.OperationKey
	req.ConfirmationID = p.ConfirmationID
	req.ArgumentsDigest = p.ArgumentsDigest
	req.Snapshot = p.Snapshot
	if _, e = rt.Claim(ctx, req); e == nil {
		t.Fatal("unconfirmed operation claimed")
	}
	if e = rt.Decide(ctx, "other", "t", p.ConfirmationID, "approved", p.ArgumentsDigest, p.Snapshot); e == nil {
		t.Fatal("foreign decision allowed")
	}
	if e = rt.Decide(ctx, "u", "t", p.ConfirmationID, "approved", p.ArgumentsDigest, p.Snapshot); e != nil {
		t.Fatal(e)
	}
	metadata, e := rt.Claim(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rt.AuthorizeDispatch(ctx, req.Execution, req.WireName, req.Arguments, metadata); e != nil {
		t.Fatal(e)
	}
	altered := json.RawMessage(`{"targets":[{"kind":"document","knowledge_base_id":"kb","document_id":"other"}]}`)
	if _, e = rt.AuthorizeDispatch(ctx, req.Execution, req.WireName, altered, metadata); e == nil {
		t.Fatal("changed arguments dispatched")
	}
	snapshot = "v2"
	if _, e = rt.Claim(ctx, req); e == nil {
		t.Fatal("changed resource claimed")
	}
	active = false
	if _, e = rt.Status(ctx, req.Execution, p.ConfirmationID); e == nil {
		t.Fatal("inactive run accepted")
	}
}
func TestCreateNeedsNoConfirmationAndEmptyToolsDeny(t *testing.T) {
	st, _ := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	defer st.Close()
	tools := []string{"knowledge.create"}
	rt, _ := New(st, func(context.Context, Execution) (Identity, error) {
		return Identity{Principal: capability.Principal{UserID: "u"}, AllowedTools: tools}, nil
	}, func(context.Context, Identity, string, json.RawMessage) (doc.ResourceSnapshot, error) {
		t.Fatal("create snapshot unnecessary")
		return doc.ResourceSnapshot{}, nil
	})
	req := PrepareRequest{Execution: Execution{UserID: "u"}, CallID: "1", WireName: "knowledge.create", Arguments: json.RawMessage(`{"name":"A"}`)}
	p, e := rt.Prepare(context.Background(), req)
	if e != nil || p.RequiresConfirmation || p.Status != "allowed" {
		t.Fatalf("%+v %v", p, e)
	}
	tools = []string{}
	req.CallID = "2"
	if _, e = rt.Prepare(context.Background(), req); e == nil {
		t.Fatal("empty tools accepted")
	}
}

func TestCommittedDeleteReplaysBeforeChangedSnapshot(t *testing.T) {
	st, _ := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	defer st.Close()
	active, committed := true, false
	snapshotCalls := 0
	rt, _ := New(st, func(context.Context, Execution) (Identity, error) {
		if !active {
			return Identity{}, errors.New("inactive")
		}
		return Identity{Principal: capability.Principal{UserID: "u"}, AllowedTools: []string{"knowledge.resource.delete"}}, nil
	}, func(context.Context, Identity, string, json.RawMessage) (doc.ResourceSnapshot, error) {
		snapshotCalls++
		if committed {
			return doc.ResourceSnapshot{}, errors.New("source gone")
		}
		return doc.ResourceSnapshot{Digest: "original"}, nil
	})
	rt.SetReceiptLookup(func(_ context.Context, id Identity, request PrepareRequest) (bool, error) {
		if id.Principal.UserID != "u" || request.OperationKey == "" {
			t.Fatal("receipt missing identity binding")
		}
		return committed, nil
	})
	ctx := context.Background()
	req := PrepareRequest{Execution: Execution{UserID: "u"}, CallID: "delete", WireName: "knowledge.resource.delete", Arguments: json.RawMessage(`{"targets":[{"kind":"document","knowledge_base_id":"kb","document_id":"d"}]}`)}
	p, err := rt.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.ConfirmationID = p.ConfirmationID
	req.OperationKey = p.OperationKey
	req.ArgumentsDigest = p.ArgumentsDigest
	req.Snapshot = p.Snapshot
	if err = rt.Decide(ctx, "u", "", p.ConfirmationID, "approved", p.ArgumentsDigest, p.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Claim(ctx, req); err != nil {
		t.Fatal(err)
	}
	before := snapshotCalls
	committed = true
	if _, err = rt.Claim(ctx, req); err != nil {
		t.Fatal(err)
	}
	if snapshotCalls != before {
		t.Fatal("committed receipt still depends on deleted source")
	}
	active = false
	if _, err = rt.Claim(ctx, req); err == nil {
		t.Fatal("receipt bypassed live ownership")
	}
}

func TestGuardCommitRejectsCancelledExecutionInsideTransaction(t *testing.T) {
	st, _ := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	defer st.Close()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE conversations (id text primary key, updated_at text)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO conversations(id) VALUES ('conversation')")
	db.Exec("CREATE TABLE mutations(id text)")
	active := true
	rt, _ := New(st, func(context.Context, Execution) (Identity, error) {
		return Identity{Principal: capability.Principal{UserID: "u"}, AllowedTools: []string{"knowledge.create"}}, nil
	}, func(context.Context, Identity, string, json.RawMessage) (doc.ResourceSnapshot, error) {
		return doc.ResourceSnapshot{}, nil
	})
	ctx := context.Background()
	req := PrepareRequest{Execution: Execution{UserID: "u", ConversationID: "conversation"}, CallID: "create", WireName: "knowledge.create", Arguments: json.RawMessage(`{"name":"new"}`)}
	p, err := rt.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.ConfirmationID = p.ConfirmationID
	req.OperationKey = p.OperationKey
	req.ArgumentsDigest = p.ArgumentsDigest
	req.Snapshot = p.Snapshot
	meta, err := rt.Claim(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := rt.AuthorizeDispatch(ctx, req.Execution, req.WireName, req.Arguments, meta)
	if err != nil {
		t.Fatal(err)
	}
	active = false // cancellation after dispatch authorization, before resource commit
	err = db.Transaction(func(tx *gorm.DB) error {
		guard := rt.GuardCommit(ctx, tx, invocation.ExecutionKey, func(_ context.Context, locked *gorm.DB, e Execution) (Identity, error) {
			if locked != tx || e.ConversationID != "conversation" {
				t.Fatal("lost commit transaction")
			}
			if !active {
				return Identity{}, errors.New("cancelled")
			}
			return Identity{AllowedTools: []string{"knowledge.create"}}, nil
		})
		if guard != nil {
			return guard
		}
		return tx.Exec("INSERT INTO mutations(id) VALUES ('created')").Error
	})
	if err == nil {
		t.Fatal("cancelled execution committed")
	}
	var count int64
	db.Table("mutations").Count(&count)
	if count != 0 {
		t.Fatal("mutation escaped guard")
	}
}

func TestRuntimeErrorPreservesBudgetDetails(t *testing.T) {
	response := httptest.NewRecorder()
	replyRuntimeError(response, &doc.ResourceError{Code: "OPERATION_TOO_LARGE", Message: "reduce roots", Details: map[string]any{"actual": 50, "limit": 20}})
	var value struct {
		Error struct {
			Code    string
			Details map[string]any
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if response.Code != 409 || value.Error.Code != "OPERATION_TOO_LARGE" || value.Error.Details["actual"] != float64(50) {
		t.Fatalf("lost details: %s", response.Body.String())
	}
}

func TestDecisionPersistenceRetryDoesNotChangeApprovedDecision(t *testing.T) {
	st, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	active := true
	rt, _ := New(st, func(context.Context, Execution) (Identity, error) {
		if !active {
			return Identity{}, errors.New("inactive")
		}
		return Identity{Principal: capability.Principal{UserID: "u", TenantID: "t"}, AllowedTools: []string{"knowledge.resource.delete"}}, nil
	}, func(context.Context, Identity, string, json.RawMessage) (doc.ResourceSnapshot, error) {
		return doc.ResourceSnapshot{Digest: "v"}, nil
	})
	ctx := context.Background()
	x := Execution{UserID: "u", HistoryID: "h", RunID: "r"}
	p, err := rt.Prepare(ctx, PrepareRequest{Execution: x, WireName: "knowledge.resource.delete", CallID: "c", Arguments: json.RawMessage(`{"targets":[{"kind":"document","knowledge_base_id":"k","document_id":"d"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	fail, durable := true, ""
	rt.SetDecisionHook(func(_ context.Context, got Execution, prepared Prepared, decision string) error {
		if got != x || prepared.ConfirmationID != p.ConfirmationID {
			t.Fatal("wrong decision binding")
		}
		if fail {
			return errors.New("history temporarily unavailable")
		}
		durable = decision
		return nil
	})
	if err = rt.Decide(ctx, "u", "t", p.ConfirmationID, "approved", p.ArgumentsDigest, p.Snapshot); err == nil {
		t.Fatal("history failure hidden")
	}
	fail, active = false, false // execution may complete before the HTTP retry
	if err = rt.Decide(ctx, "u", "t", p.ConfirmationID, "approved", p.ArgumentsDigest, p.Snapshot); err != nil || durable != "approved" {
		t.Fatalf("decision retry: %s %v", durable, err)
	}
	if err = rt.Decide(ctx, "u", "t", p.ConfirmationID, "rejected", p.ArgumentsDigest, p.Snapshot); err == nil || durable != "approved" {
		t.Fatal("changed a completed decision")
	}
}
