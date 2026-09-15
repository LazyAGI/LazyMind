package vocabulary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ankiClient struct {
	endpoint string
	client   *http.Client
}

type ankiEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  any             `json:"error"`
}

type ankiNoteInfo struct {
	NoteID int64 `json:"noteId"`
	Fields map[string]struct {
		Value string `json:"value"`
	} `json:"fields"`
	Tags []string `json:"tags"`
}

type AnkiDeck struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

func newAnkiClient(endpoint string) (*ankiClient, error) {
	if bridgeEndpoint := strings.TrimSpace(os.Getenv("LAZYMIND_ANKI_CONNECT_URL")); bridgeEndpoint != "" {
		endpoint = bridgeEndpoint
	}
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme != "http" || u.Port() == "" {
		return nil, errors.New("invalid AnkiConnect endpoint")
	}
	host := strings.ToLower(u.Hostname())
	configuredBridge := strings.TrimSpace(os.Getenv("LAZYMIND_ANKI_CONNECT_URL"))
	if host != "localhost" && host != "127.0.0.1" && host != "::1" && endpoint != configuredBridge {
		return nil, errors.New("AnkiConnect endpoint must use loopback")
	}
	return &ankiClient{endpoint: u.String(), client: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext},
	}}, nil
}

func (c *ankiClient) invoke(ctx context.Context, action string, params any, out any) error {
	body, err := json.Marshal(map[string]any{"action": action, "version": 6, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("AnkiConnect unavailable: %w", err)
	}
	defer resp.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AnkiConnect returned HTTP %d", resp.StatusCode)
	}
	var envelope ankiEnvelope
	if err := json.Unmarshal(limited, &envelope); err != nil {
		return errors.New("AnkiConnect returned invalid JSON")
	}
	if envelope.Error != nil {
		return fmt.Errorf("AnkiConnect: %v", envelope.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

func (c *ankiClient) version(ctx context.Context) (int, error) {
	var version int
	err := c.invoke(ctx, "version", map[string]any{}, &version)
	return version, err
}

func (c *ankiClient) modelNames(ctx context.Context) ([]string, error) {
	var names []string
	err := c.invoke(ctx, "modelNames", map[string]any{}, &names)
	return names, err
}
func (c *ankiClient) deckNames(ctx context.Context) ([]string, error) {
	var names []string
	err := c.invoke(ctx, "deckNames", map[string]any{}, &names)
	return names, err
}
func (c *ankiClient) decks(ctx context.Context) ([]AnkiDeck, error) {
	var ids map[string]int64
	if err := c.invoke(ctx, "deckNamesAndIds", map[string]any{}, &ids); err != nil {
		return nil, err
	}
	decks := make([]AnkiDeck, 0, len(ids))
	for name, id := range ids {
		decks = append(decks, AnkiDeck{ID: id, Name: name, IsDefault: id == 1})
	}
	sort.Slice(decks, func(i, j int) bool { return decks[i].Name < decks[j].Name })
	return decks, nil
}
func (c *ankiClient) createDeck(ctx context.Context, name string) error {
	var id int64
	return c.invoke(ctx, "createDeck", map[string]any{"deck": name}, &id)
}
func (c *ankiClient) deleteDeck(ctx context.Context, name, mode, target string) error {
	decks, err := c.decks(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, deck := range decks {
		if deck.Name == name {
			found = true
			if deck.IsDefault {
				return errors.New("Anki system default deck cannot be deleted")
			}
			break
		}
	}
	if !found {
		return errors.New("Anki deck not found")
	}
	var cards []int64
	query := "deck:\"" + strings.ReplaceAll(name, "\"", "") + "\""
	if err := c.invoke(ctx, "findCards", map[string]any{"query": query}, &cards); err != nil {
		return err
	}
	if mode != "delete_words" && len(cards) > 0 {
		if strings.TrimSpace(target) == "" || target == name {
			return errors.New("target wordbook must be different")
		}
		if err := c.invoke(ctx, "changeDeck", map[string]any{"cards": cards, "deck": target}, nil); err != nil {
			return err
		}
	}
	return c.invoke(ctx, "deleteDecks", map[string]any{"decks": []string{name}, "cardsToo": true}, nil)
}
func (c *ankiClient) vocabularyNotes(ctx context.Context, deck string) ([]ankiNoteInfo, error) {
	var ids []int64
	query := "deck:\"" + strings.ReplaceAll(deck, "\"", "") + "\" note:\"" + vocabularyModel + "\""
	if err := c.invoke(ctx, "findNotes", map[string]any{"query": query}, &ids); err != nil || len(ids) == 0 {
		return nil, err
	}
	var notes []ankiNoteInfo
	err := c.invoke(ctx, "notesInfo", map[string]any{"notes": ids}, &notes)
	return notes, err
}
func (c *ankiClient) requestPermission(ctx context.Context) (string, error) {
	var result struct {
		Permission    string `json:"permission"`
		RequireApiKey bool   `json:"requireApiKey"`
	}
	err := c.invoke(ctx, "requestPermission", map[string]any{}, &result)
	return result.Permission, err
}
func (c *ankiClient) actionNames(ctx context.Context) ([]string, error) {
	var result struct {
		Actions []string `json:"actions"`
	}
	err := c.invoke(ctx, "apiReflect", map[string]any{"scopes": []string{"actions"}}, &result)
	return result.Actions, err
}

func (c *ankiClient) initialize(ctx context.Context, deck string) error {
	if err := c.invoke(ctx, "createDeck", map[string]any{"deck": deck}, nil); err != nil {
		return err
	}
	names, err := c.modelNames(ctx)
	if err != nil {
		return err
	}
	has := func(target string) bool {
		for _, name := range names {
			if name == target {
				return true
			}
		}
		return false
	}
	if !has(vocabularyModel) {
		params := map[string]any{
			"modelName":     vocabularyModel,
			"inOrderFields": []string{"LazyMindID", "Word", "Language", "Phonetic", "PartOfSpeech", "Meaning", "Definition", "DictionarySource", "UserNote", "SourceRefsJSON", "SchemaVersion"},
			"css":           ".card{font-family:Arial;font-size:22px;text-align:center;color:#222;background:#fff}.meaning{margin-top:16px;font-size:18px}",
			"cardTemplates": []map[string]string{{"Name": "Word → Meaning", "Front": "<b>{{Word}}</b><div>{{Phonetic}}</div>", "Back": "{{FrontSide}}<hr><div class=meaning>{{Meaning}}</div><div>{{Definition}}</div>"}},
		}
		if err := c.invoke(ctx, "createModel", params, nil); err != nil {
			return err
		}
	}
	if !has(sentenceModel) {
		params := map[string]any{
			"modelName":     sentenceModel,
			"inOrderFields": []string{"LazyMindID", "VocabularyID", "TargetWord", "Sentence", "Translation", "ContentOrigin", "SourceRefsJSON", "SchemaVersion"},
			"css":           ".card{font-family:Arial;font-size:20px;text-align:left;color:#222;background:#fff}.translation{margin-top:16px;color:#555}",
			"cardTemplates": []map[string]string{{"Name": "Sentence", "Front": "{{Sentence}}", "Back": "{{FrontSide}}<hr><b>{{TargetWord}}</b><div class=translation>{{Translation}}</div>"}},
		}
		if err := c.invoke(ctx, "createModel", params, nil); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeTags(tags []string, kind string) []string {
	result := []string{"lazymind", "kind::" + kind}
	seen := map[string]bool{"lazymind": true, "kind::" + kind: true}
	for _, raw := range tags {
		tag := strings.Join(strings.Fields(strings.TrimSpace(raw)), "_")
		if tag != "" && !seen[tag] {
			seen[tag] = true
			result = append(result, tag)
		}
	}
	return result
}

func (c *ankiClient) addNote(ctx context.Context, deck, model string, fields map[string]string, tags []string) (string, error) {
	var id int64
	err := c.invoke(ctx, "addNote", map[string]any{"note": map[string]any{
		"deckName": deck, "modelName": model, "fields": fields, "tags": tags,
		"options": map[string]any{"allowDuplicate": false},
	}}, &id)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

func (c *ankiClient) sync(ctx context.Context) error {
	return c.invoke(ctx, "sync", map[string]any{}, nil)
}

func (c *ankiClient) deleteNote(ctx context.Context, noteID string) error {
	id, err := strconv.ParseInt(noteID, 10, 64)
	if err != nil {
		return err
	}
	return c.invoke(ctx, "deleteNotes", map[string]any{"notes": []int64{id}}, nil)
}
func (c *ankiClient) suspendNote(ctx context.Context, noteID string) error {
	var cards []int64
	if err := c.invoke(ctx, "findCards", map[string]any{"query": "nid:" + noteID}, &cards); err != nil {
		return err
	}
	return c.invoke(ctx, "suspend", map[string]any{"cards": cards}, nil)
}
func (c *ankiClient) updateNote(ctx context.Context, noteID string, fields map[string]string) error {
	id, err := strconv.ParseInt(noteID, 10, 64)
	if err != nil {
		return err
	}
	return c.invoke(ctx, "updateNoteFields", map[string]any{"note": map[string]any{"id": id, "fields": fields}}, nil)
}
func (c *ankiClient) updateTags(ctx context.Context, noteID string, tags []string) error {
	id, err := strconv.ParseInt(noteID, 10, 64)
	if err != nil {
		return err
	}
	if err := c.invoke(ctx, "removeTags", map[string]any{"notes": []int64{id}, "tags": "lazymind kind::vocabulary state::weak"}, nil); err != nil {
		return err
	}
	return c.invoke(ctx, "addTags", map[string]any{"notes": []int64{id}, "tags": strings.Join(tags, " ")}, nil)
}

type ankiCardInfo struct {
	CardID    int64  `json:"cardId"`
	Queue     int    `json:"queue"`
	Due       any    `json:"due"`
	Interval  int    `json:"interval"`
	Reps      int    `json:"reps"`
	Lapses    int    `json:"lapses"`
	Answer    string `json:"answer"`
	Question  string `json:"question"`
	ModelName string `json:"modelName"`
	Fields    map[string]struct {
		Value string `json:"value"`
	} `json:"fields"`
}

func (c *ankiClient) cardsForNote(ctx context.Context, noteID string) ([]ankiCardInfo, error) {
	var ids []int64
	if err := c.invoke(ctx, "findCards", map[string]any{"query": "nid:" + noteID}, &ids); err != nil {
		return nil, err
	}
	var cards []ankiCardInfo
	if len(ids) == 0 {
		return cards, nil
	}
	err := c.invoke(ctx, "cardsInfo", map[string]any{"cards": ids}, &cards)
	return cards, err
}
func (c *ankiClient) answerCard(ctx context.Context, cardID int64, ease int) error {
	var result []bool
	if err := c.invoke(ctx, "answerCards", map[string]any{"answers": []map[string]any{{"cardId": cardID, "ease": ease}}}, &result); err != nil {
		return err
	}
	if len(result) != 1 || !result[0] {
		return errors.New("AnkiConnect did not accept the answer")
	}
	return nil
}
