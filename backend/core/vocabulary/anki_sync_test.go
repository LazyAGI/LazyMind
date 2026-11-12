package vocabulary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestAnkiOutboxMirrorsLocalWordOnlyAfterOptIn(t *testing.T) {
	s := testService(t)
	t.Setenv("LAZYMIND_VOCABULARY_ANKI_SYNC_ENABLED", "true")
	t.Setenv("LAZYMIND_ANKI_CONNECT_URL", "")
	ctx := context.Background()
	var calls, adds atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result any
		switch req.Action {
		case "modelNames":
			result = []string{vocabularyModel, sentenceModel}
		case "findNotes":
			result = []int64{}
		case "addNote":
			adds.Add(1)
			result = 42
		case "createDeck", "sync":
		default:
			t.Errorf("unexpected Anki action %s", req.Action)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer remote.Close()
	enabled := true
	endpoint := remote.URL
	setting, err := s.patchSetting(ctx, "u", providerSettingsPatch{AnkiEndpoint: &endpoint, AnkiSyncEnabled: &enabled})
	if err != nil || !setting.AnkiSyncEnabled || setting.SelectedProvider != "local" {
		t.Fatalf("setting=%+v err=%v", setting, err)
	}
	result, err := s.AddWord(ctx, "u", AddWordRequest{Term: "offline", Meaning: "available"})
	if err != nil || result.Word.ID == "" || result.Word.Provider != "local" || !result.Queued {
		t.Fatalf("local save result=%+v err=%v", result, err)
	}
	if _, err := s.GetWord(ctx, "u", result.Word.ID); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("local requests contacted Anki %d times", got)
	}
	otherEndpoint := "http://127.0.0.1:1"
	if _, err := s.patchSetting(ctx, "u", providerSettingsPatch{AnkiEndpoint: &otherEndpoint}); err == nil {
		t.Fatal("changing Anki target with pending operations should fail")
	}
	disabled := false
	if _, err := s.patchSetting(ctx, "u", providerSettingsPatch{AnkiSyncEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	s.drainAnkiOutbox(ctx)
	if err := s.Sync(ctx, "u"); err == nil || calls.Load() != 0 {
		t.Fatalf("disabled sync err=%v remote calls=%d", err, calls.Load())
	}
	if _, err := s.patchSetting(ctx, "u", providerSettingsPatch{AnkiSyncEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	s.drainAnkiOutbox(ctx)
	if got := adds.Load(); got != 1 {
		t.Fatalf("worker created %d remote notes", got)
	}
	item, err := s.GetWord(ctx, "u", result.Word.ID)
	if err != nil || item.Word.ProviderNoteID != "42" {
		t.Fatalf("mirrored word=%+v err=%v", item, err)
	}
	s.drainAnkiOutbox(ctx)
	if got := adds.Load(); got != 1 {
		t.Fatalf("worker retried an already completed operation, %d remote notes", got)
	}
}
