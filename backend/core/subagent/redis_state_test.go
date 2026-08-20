package subagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/state"
)

// mockStateStore implements state.Store for testing Redis-backed functions.
type mockStateStore struct {
	// HSet
	hsetCalls []hsetCall
	hsetErr   error
	// HGetAll
	hgetallResult map[string]string
	hgetallErr    error
	// RPush
	rpushCalls []rpushCall
	rpushErr   error
	// LRange
	lrangeResult []string
	lrangeErr    error
	// Exists
	existsResult bool
	existsErr    error
}

type hsetCall struct {
	key    string
	fields map[string]any
}

type rpushCall struct {
	key   string
	value []byte
}

func (m *mockStateStore) HSet(_ context.Context, key string, fields map[string]any, _ time.Duration) error {
	m.hsetCalls = append(m.hsetCalls, hsetCall{key: key, fields: fields})
	return m.hsetErr
}
func (m *mockStateStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	return m.hgetallResult, m.hgetallErr
}
func (m *mockStateStore) HGet(_ context.Context, key, field string) ([]byte, error)  { return nil, nil }
func (m *mockStateStore) HDel(_ context.Context, key string, fields ...string) error { return nil }
func (m *mockStateStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	return nil
}
func (m *mockStateStore) Get(_ context.Context, key string) ([]byte, error) { return nil, nil }
func (m *mockStateStore) Del(_ context.Context, keys ...string) error       { return nil }
func (m *mockStateStore) Exists(_ context.Context, key string) (bool, error) {
	return m.existsResult, m.existsErr
}
func (m *mockStateStore) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return false, nil
}
func (m *mockStateStore) RPush(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.rpushCalls = append(m.rpushCalls, rpushCall{key: key, value: value})
	return m.rpushErr
}
func (m *mockStateStore) LPush(_ context.Context, key string, value []byte, ttl time.Duration) error {
	return nil
}
func (m *mockStateStore) LRange(_ context.Context, key string, start, stop int64) ([]string, error) {
	return m.lrangeResult, m.lrangeErr
}
func (m *mockStateStore) LTrim(_ context.Context, key string, start, stop int64) error { return nil }
func (m *mockStateStore) LPop(_ context.Context, key string) (bool, error) {
	return false, nil
}
func (m *mockStateStore) ZAdd(_ context.Context, key, member string, score float64, ttl time.Duration) error {
	return nil
}
func (m *mockStateStore) ZRemRangeByScore(_ context.Context, key string, min, max float64) error {
	return nil
}
func (m *mockStateStore) ZCard(_ context.Context, key string) (int64, error) { return 0, nil }
func (m *mockStateStore) Close() error                                       { return nil }

// Compile-time check: mockStateStore implements state.Store.
var _ state.Store = (*mockStateStore)(nil)

// --- WriteStatus ---

// TestWriteStatus_NilStore returns nil without calling HSet.
func TestWriteStatus_NilStore(t *testing.T) {
	if err := WriteStatus(context.Background(), nil, "task-1", map[string]any{"status": "ok"}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestWriteStatus_CallsHSetWithCorrectKey uses the status key format and forwards fields.
func TestWriteStatus_CallsHSetWithCorrectKey(t *testing.T) {
	mock := &mockStateStore{}
	fields := map[string]any{"status": "running", "progress": 50}
	if err := WriteStatus(context.Background(), mock, "task-abc", fields); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.hsetCalls) != 1 {
		t.Fatalf("expected 1 HSet call, got %d", len(mock.hsetCalls))
	}
	call := mock.hsetCalls[0]
	wantKey := "rag/subagent/status:task-abc"
	if call.key != wantKey {
		t.Fatalf("key: got %q, want %q", call.key, wantKey)
	}
	if call.fields["status"] != "running" || call.fields["progress"] != 50 {
		t.Fatalf("fields mismatch: %v", call.fields)
	}
}

// TestWriteStatus_PropagatesHSetError returns the underlying error.
func TestWriteStatus_PropagatesHSetError(t *testing.T) {
	mock := &mockStateStore{hsetErr: context.DeadlineExceeded}
	err := WriteStatus(context.Background(), mock, "task-1", map[string]any{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- ReadStatus ---

// TestReadStatus_NilStore returns nil map and nil error.
func TestReadStatus_NilStore(t *testing.T) {
	result, err := ReadStatus(context.Background(), nil, "task-1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
}

// TestReadStatus_ReturnsHGetAllResult uses the status key and returns mock data.
func TestReadStatus_ReturnsHGetAllResult(t *testing.T) {
	mock := &mockStateStore{
		hgetallResult: map[string]string{"status": "done", "progress": "100"},
	}
	result, err := ReadStatus(context.Background(), mock, "task-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["status"] != "done" || result["progress"] != "100" {
		t.Fatalf("result mismatch: %v", result)
	}
}

// TestReadStatus_PropagatesHGetAllError returns the underlying error.
func TestReadStatus_PropagatesHGetAllError(t *testing.T) {
	mock := &mockStateStore{hgetallErr: context.DeadlineExceeded}
	_, err := ReadStatus(context.Background(), mock, "task-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- AppendStreamEvent ---

// TestAppendStreamEvent_NilStore returns nil.
func TestAppendStreamEvent_NilStore(t *testing.T) {
	if err := AppendStreamEvent(context.Background(), nil, "task-1", "event"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestAppendStreamEvent_RPushesJSON serializes and RPUSHes the event.
func TestAppendStreamEvent_RPushesJSON(t *testing.T) {
	mock := &mockStateStore{}
	event := map[string]any{"type": "text", "content": "hello"}
	if err := AppendStreamEvent(context.Background(), mock, "task-1", event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.rpushCalls) != 1 {
		t.Fatalf("expected 1 RPush call, got %d", len(mock.rpushCalls))
	}
	call := mock.rpushCalls[0]
	wantKey := "rag/subagent/stream:task-1"
	if call.key != wantKey {
		t.Fatalf("key: got %q, want %q", call.key, wantKey)
	}
	// Verify the value is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(call.value, &parsed); err != nil {
		t.Fatalf("RPushed value is not valid JSON: %s", string(call.value))
	}
	if parsed["type"] != "text" {
		t.Fatalf("event type mismatch: %v", parsed)
	}
}

// TestAppendStreamEvent_UnmarshalableValue returns error for non-JSON-serializable types.
func TestAppendStreamEvent_UnmarshalableValue(t *testing.T) {
	mock := &mockStateStore{}
	// A channel cannot be JSON-marshaled.
	err := AppendStreamEvent(context.Background(), mock, "task-1", make(chan int))
	if err == nil {
		t.Fatal("expected error for unmarshalable value, got nil")
	}
}

// TestAppendStreamEvent_PropagatesRPushError returns the underlying error.
func TestAppendStreamEvent_PropagatesRPushError(t *testing.T) {
	mock := &mockStateStore{rpushErr: context.DeadlineExceeded}
	err := AppendStreamEvent(context.Background(), mock, "task-1", "event")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- StreamEventsFrom ---

// TestStreamEventsFrom_NilStore returns nil slice and nil error.
func TestStreamEventsFrom_NilStore(t *testing.T) {
	result, err := StreamEventsFrom(context.Background(), nil, "task-1", 0)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
}

// TestStreamEventsFrom_CallsLRange uses stream key and from offset.
func TestStreamEventsFrom_CallsLRange(t *testing.T) {
	mock := &mockStateStore{
		lrangeResult: []string{`{"type":"text","content":"hello"}`},
	}
	result, err := StreamEventsFrom(context.Background(), mock, "task-1", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != `{"type":"text","content":"hello"}` {
		t.Fatalf("result mismatch: %v", result)
	}
}

// TestStreamEventsFrom_PropagatesLRangeError returns the underlying error.
func TestStreamEventsFrom_PropagatesLRangeError(t *testing.T) {
	mock := &mockStateStore{lrangeErr: context.DeadlineExceeded}
	_, err := StreamEventsFrom(context.Background(), mock, "task-1", 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- StreamExists ---

// TestStreamExists_NilStore returns false and nil error.
func TestStreamExists_NilStore(t *testing.T) {
	exists, err := StreamExists(context.Background(), nil, "task-1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if exists {
		t.Fatal("expected false for nil store")
	}
}

// TestStreamExists_ReturnsMockValue forwards the Exists result.
func TestStreamExists_ReturnsMockValue(t *testing.T) {
	mock := &mockStateStore{existsResult: true}
	exists, err := StreamExists(context.Background(), mock, "task-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("expected true")
	}
}

// TestStreamExists_PropagatesExistsError returns the underlying error.
func TestStreamExists_PropagatesExistsError(t *testing.T) {
	mock := &mockStateStore{existsErr: context.DeadlineExceeded}
	_, err := StreamExists(context.Background(), mock, "task-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
