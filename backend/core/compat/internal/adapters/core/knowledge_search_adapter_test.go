package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/chat"
	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
)

type fakeKnowledgeChatRunner struct {
	input  chat.KnowledgeChatRequest
	result chat.KnowledgeChatResult
	err    error
	calls  int
}

func (r *fakeKnowledgeChatRunner) RunKnowledgeChat(ctx context.Context, input chat.KnowledgeChatRequest) (chat.KnowledgeChatResult, error) {
	r.calls++
	r.input = input
	if r.err != nil {
		return chat.KnowledgeChatResult{}, r.err
	}
	return r.result, nil
}

type fakeDatasetGetter struct {
	req   doc.DatasetGetRequest
	err   error
	calls int
}

func (g *fakeDatasetGetter) GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error) {
	g.calls++
	g.req = req
	if g.err != nil {
		return doc.Dataset{}, g.err
	}
	return doc.Dataset{DatasetID: req.DatasetID}, nil
}

func TestKnowledgeSearchAdapterMapsRequestAndResult(t *testing.T) {
	runner := &fakeKnowledgeChatRunner{result: chat.KnowledgeChatResult{
		Answer:         "answer",
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		Sources: []chat.KnowledgeChatSource{
			{KnowledgeID: "ds-1", DocumentID: "", ChunkID: "chunk-1", Title: "A", Text: "alpha", Number: 1},
			{KnowledgeID: "ds-2", DocumentID: "doc-core-2", ChunkID: "chunk-2", Title: "B", Text: "beta", Number: 2},
		},
	}}
	adapter := mustKnowledgeSearchAdapter(t, runner)
	got, err := adapter.Search(context.Background(), contract.CallContext{UserID: " user-1 "}, compatknowledge.SearchInput{
		Query:          " q ",
		KnowledgeIDs:   []string{"ds-1", "ds-2"},
		ConversationID: " conv-1 ",
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if runner.input.UserID != "user-1" || runner.input.Query != "q" || runner.input.ConversationID != "conv-1" {
		t.Fatalf("unexpected runner input: %#v", runner.input)
	}
	if runner.input.UseMemory || runner.input.EnablePlugin {
		t.Fatalf("memory/plugin must default false: %#v", runner.input)
	}
	if len(runner.input.KnowledgeIDs) != 2 || runner.input.KnowledgeIDs[0] != "ds-1" || runner.input.KnowledgeIDs[1] != "ds-2" {
		t.Fatalf("unexpected knowledge ids: %#v", runner.input.KnowledgeIDs)
	}
	if got.Answer != "answer" || got.ConversationID != "conv-1" || got.MessageID != "msg-1" {
		t.Fatalf("unexpected result: %#v", got)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %#v, want 2", got.Sources)
	}
	if got.Sources[0].DocumentID != "" || got.Sources[0].ChunkID != "chunk-1" || got.Sources[1].DocumentID != "doc-core-2" {
		t.Fatalf("unexpected sources mapping: %#v", got.Sources)
	}
}

func TestKnowledgeSearchAdapterValidation(t *testing.T) {
	adapter := mustKnowledgeSearchAdapter(t, &fakeKnowledgeChatRunner{})
	_, err := adapter.Search(context.Background(), contract.CallContext{UserID: " "}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestKnowledgeSearchAdapterMapsChatErrors(t *testing.T) {
	cause := errors.New("deadline")
	tests := []struct {
		name string
		err  error
		want contract.ErrorCode
	}{
		{name: "invalid", err: &chat.KnowledgeChatError{Code: chat.KnowledgeChatInvalidArgument, Message: "bad", Cause: cause}, want: contract.InvalidArgument},
		{name: "not found", err: &chat.KnowledgeChatError{Code: chat.KnowledgeChatNotFound, Message: "missing", Cause: cause}, want: contract.NotFound},
		{name: "forbidden", err: &chat.KnowledgeChatError{Code: chat.KnowledgeChatForbidden, Message: "forbidden", Cause: cause}, want: contract.NotFound},
		{name: "unavailable", err: &chat.KnowledgeChatError{Code: chat.KnowledgeChatBackendUnavailable, Message: "down", Cause: cause}, want: contract.BackendUnavailable},
		{name: "internal", err: &chat.KnowledgeChatError{Code: chat.KnowledgeChatInternal, Message: "bad", Cause: cause}, want: contract.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := mustKnowledgeSearchAdapter(t, &fakeKnowledgeChatRunner{err: tt.err})
			_, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}})
			if code, ok := contract.CodeOf(err); !ok || code != tt.want {
				t.Fatalf("code = %v, %v; want %s", code, ok, tt.want)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("mapped error does not retain cause: %v", err)
			}
		})
	}
}

func TestKnowledgeSearchAdapterRejectsNilRunner(t *testing.T) {
	if _, err := NewKnowledgeSearchAdapter(nil); err == nil {
		t.Fatalf("NewKnowledgeSearchAdapter nil runner error = nil, want error")
	}
}

func TestKnowledgeSearchAdapterForDBRequiresDependencies(t *testing.T) {
	if _, err := NewKnowledgeSearchAdapterForDB(nil, "http://chat"); err == nil {
		t.Fatalf("NewKnowledgeSearchAdapterForDB nil db error = nil, want error")
	}
	if _, err := NewKnowledgeSearchAdapterForDB(&gorm.DB{}, " "); err == nil {
		t.Fatalf("NewKnowledgeSearchAdapterForDB empty chat endpoint error = nil, want error")
	}
}

func TestKnowledgeAccessCheckerPassesDatasetRequest(t *testing.T) {
	getter := &fakeDatasetGetter{}
	checker, err := NewKnowledgeAccessChecker(getter)
	if err != nil {
		t.Fatalf("NewKnowledgeAccessChecker: %v", err)
	}
	if err := checker.EnsureKnowledgeReadable(context.Background(), " user-1 ", " ds-1 "); err != nil {
		t.Fatalf("EnsureKnowledgeReadable: %v", err)
	}
	if getter.calls != 1 {
		t.Fatalf("getter calls = %d, want 1", getter.calls)
	}
	if getter.req.UserID != "user-1" || getter.req.DatasetID != "ds-1" || getter.req.Caller.UserID != "user-1" {
		t.Fatalf("unexpected dataset request: %#v", getter.req)
	}
}

func TestKnowledgeAccessCheckerMapsDatasetErrors(t *testing.T) {
	cause := errors.New("db down")
	tests := []struct {
		name string
		err  error
		want chat.KnowledgeChatErrorCode
	}{
		{name: "not found", err: &doc.DatasetServiceError{Code: doc.DatasetServiceNotFound, Message: "missing", Err: gorm.ErrRecordNotFound}, want: chat.KnowledgeChatNotFound},
		{name: "forbidden", err: &doc.DatasetServiceError{Code: doc.DatasetServiceForbidden, Message: "forbidden"}, want: chat.KnowledgeChatNotFound},
		{name: "unavailable", err: &doc.DatasetServiceError{Code: doc.DatasetServiceUnavailable, Message: "db", Err: cause}, want: chat.KnowledgeChatBackendUnavailable},
		{name: "internal", err: &doc.DatasetServiceError{Code: doc.DatasetServiceInternal, Message: "bad", Err: cause}, want: chat.KnowledgeChatInternal},
		{name: "unknown", err: cause, want: chat.KnowledgeChatInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, err := NewKnowledgeAccessChecker(&fakeDatasetGetter{err: tt.err})
			if err != nil {
				t.Fatalf("NewKnowledgeAccessChecker: %v", err)
			}
			err = checker.EnsureKnowledgeReadable(context.Background(), "user-1", "ds-1")
			var chatErr *chat.KnowledgeChatError
			if !errors.As(err, &chatErr) || chatErr.Code != tt.want {
				t.Fatalf("code = %#v, want %s", err, tt.want)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("mapped error does not retain cause: %v", err)
			}
		})
	}
}

func TestKnowledgeAccessCheckerRejectsNilAndInvalidInputs(t *testing.T) {
	if _, err := NewKnowledgeAccessChecker(nil); err == nil {
		t.Fatalf("NewKnowledgeAccessChecker nil service error = nil, want error")
	}
	checker, err := NewKnowledgeAccessChecker(&fakeDatasetGetter{})
	if err != nil {
		t.Fatalf("NewKnowledgeAccessChecker: %v", err)
	}
	err = checker.EnsureKnowledgeReadable(context.Background(), "", "ds-1")
	var chatErr *chat.KnowledgeChatError
	if !errors.As(err, &chatErr) || chatErr.Code != chat.KnowledgeChatInvalidArgument {
		t.Fatalf("code = %#v, want invalid argument", err)
	}
}

func TestKnowledgeSearchAdapterDoesNotExposeInternalSourceFields(t *testing.T) {
	runner := &fakeKnowledgeChatRunner{result: chat.KnowledgeChatResult{
		Sources: []chat.KnowledgeChatSource{{KnowledgeID: "ds-1", DocumentID: "", ChunkID: "chunk-1", Title: "title", Text: "text", Number: 1}},
	}}
	adapter := mustKnowledgeSearchAdapter(t, runner)
	got, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	encoded, err := json.Marshal(got.Sources)
	if err != nil {
		t.Fatalf("marshal sources: %v", err)
	}
	raw := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"lazyllm", "local_path", "metadata", "global_metadata", "docid"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("source leaked %q: %s", forbidden, raw)
		}
	}
}

func mustKnowledgeSearchAdapter(t *testing.T, runner KnowledgeChatRunner) *KnowledgeSearchAdapter {
	t.Helper()
	adapter, err := NewKnowledgeSearchAdapter(runner)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchAdapter: %v", err)
	}
	return adapter
}
