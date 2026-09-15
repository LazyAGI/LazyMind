package vocabulary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAnkiClientLocalIntegration(t *testing.T) {
	if os.Getenv("ANKI_INTEGRATION") != "1" {
		t.Skip("set ANKI_INTEGRATION=1 with AnkiConnect running")
	}
	client, err := newAnkiClient(defaultAnkiURL)
	if err != nil {
		t.Fatal(err)
	}
	if version, err := client.version(context.Background()); err != nil || version < 6 {
		t.Fatalf("version = %d, err = %v", version, err)
	}
	if err := client.initialize(context.Background(), defaultAnkiDeck); err != nil {
		t.Fatal(err)
	}
	names, err := client.modelNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if !seen[vocabularyModel] || !seen[sentenceModel] {
		t.Fatalf("LazyMind models were not created: %v", names)
	}
}

func TestNewAnkiClientRejectsNonLoopback(t *testing.T) {
	if _, err := newAnkiClient("http://example.com:8765"); err == nil {
		t.Fatal("expected non-loopback endpoint to be rejected")
	}
}

func TestAnkiClientInitializeCreatesMissingModels(t *testing.T) {
	actions := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, request.Action)
		result := any(int64(1))
		if request.Action == "modelNames" {
			result = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()

	client, err := newAnkiClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.initialize(context.Background(), "LazyMind Test"); err != nil {
		t.Fatal(err)
	}
	want := []string{"createDeck", "modelNames", "createModel", "createModel"}
	if len(actions) != len(want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("actions = %v, want %v", actions, want)
		}
	}
}

func TestSanitizeTags(t *testing.T) {
	got := sanitizeTags([]string{"technical term", "lazymind", "technical term"}, "vocabulary")
	want := []string{"lazymind", "kind::vocabulary", "technical_term"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

func TestAnkiClientListsAndCreatesDecks(t *testing.T) {
	actions := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, request.Action)
		result := any(int64(42))
		if request.Action == "deckNames" {
			result = []string{"Default", "Reading"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()
	client, err := newAnkiClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	decks, err := client.deckNames(context.Background())
	if err != nil || len(decks) != 2 || decks[1] != "Reading" {
		t.Fatalf("decks=%v err=%v", decks, err)
	}
	if err := client.createDeck(context.Background(), "New Words"); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || actions[0] != "deckNames" || actions[1] != "createDeck" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestAnkiClientIdentifiesDefaultDeckByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]int64{"Default": 42, "系统默认": 1},
			"error":  nil,
		})
	}))
	defer server.Close()
	client, err := newAnkiClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	decks, err := client.decks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(decks) != 2 || decks[0].Name != "Default" || decks[0].IsDefault || decks[1].Name != "系统默认" || !decks[1].IsDefault {
		t.Fatalf("decks=%+v", decks)
	}
}

func TestAnkiClientMovesCardsBeforeDeletingDeck(t *testing.T) {
	actions := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, request.Action)
		result := any(nil)
		if request.Action == "deckNamesAndIds" {
			result = map[string]int64{"Default": 1, "Reading": 2}
		} else if request.Action == "findCards" {
			result = []int64{10, 11}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()
	client, err := newAnkiClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.deleteDeck(context.Background(), "Reading", "move", "Default"); err != nil {
		t.Fatal(err)
	}
	want := []string{"deckNamesAndIds", "findCards", "changeDeck", "deleteDecks"}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("actions=%v", actions)
		}
	}
}
