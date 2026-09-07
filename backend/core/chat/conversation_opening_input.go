package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type openingSnapshot struct {
	Input        json.RawMessage
	IDs          []string
	Hash         string
	Evidence     string
	Turns        int
	DefaultTitle string
	Active       bool
}

// Bound model calls when a historical conversation starts with only semantic-empty chatter.
const maxOpeningScannedTurns = 12

type openingEvidenceRow struct {
	ID, RawContent, Result string
	Ext                    json.RawMessage
}

func openingHash(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func openingEvidenceRows(rows []openingEvidenceRow) []openingEvidenceRow {
	for i := range rows {
		files := openingAttachments(rows[i].Ext)
		for _, file := range files {
			delete(file, "description")
			delete(file, "description_status")
		}
		rows[i].Ext, _ = json.Marshal(files)
		rows[i].Result = openingClarification(rows[i].Result)
	}
	return rows
}

func openingEvidence(db *gorm.DB, conv orm.Conversation, ids []string) (string, error) {
	rows := []openingEvidenceRow{}
	err := db.Model(&orm.ChatHistory{}).Select("id, raw_content, result, ext").Where("conversation_id = ? AND id IN ?", conv.ID, ids).Order("seq ASC, create_time ASC, id ASC").Find(&rows).Error
	return openingHash([]any{openingEvidenceRows(rows), conv.SourceContext, conv.SourceSelectedText}), err
}

var openingSourceReference = regexp.MustCompile(`这个|那个|这段|这份|这些|上述|前面|刚才|上面|第.{1,3}(个|种)|照此|按此|(?i)\b(this|that|above|previous)\b`)
var openingDataURI = regexp.MustCompile(`data:[^\s,]+;base64,[A-Za-z0-9+/=]+`)

func openingText(text string) string {
	return openingDataURI.ReplaceAllString(stripThinkTags(stripToolTags(text)), "[attachment]")
}
func openingClarification(text string) string {
	text = strings.TrimSpace(openingText(text))
	if len([]rune(text)) <= 2000 && strings.ContainsAny(text, "?？") {
		return text
	}
	return ""
}

func openingGreeting(text string) bool {
	switch strings.ToLower(strings.Trim(text, " \n\t!！。.,，?？")) {
	case "", "你好", "您好", "嗨", "hi", "hello", "hey":
		return true
	}
	return false
}

func openingAttachments(ext json.RawMessage) []map[string]any {
	var data struct {
		Input []map[string]any `json:"input"`
	}
	_ = json.Unmarshal(ext, &data)
	var out []map[string]any
	for _, part := range data.Input {
		kind, _ := part["input_type"].(string)
		if kind == "text" {
			continue
		}
		uri, _ := part["uri"].(string)
		if uri == "" || strings.HasPrefix(uri, "data:") {
			continue
		}
		file := map[string]any{"name": uri, "description_status": "unavailable"}
		// Only persisted descriptions qualify; filenames and paths are not file contents.
		if description, ok := part["description"].(string); ok && strings.TrimSpace(description) != "" {
			file["description"], file["description_status"] = description, "available"
		}
		out = append(out, file)
	}
	return out
}

// Reuse image descriptions already saved with the answer; never execute tools here.
func openingAttachmentDescriptions(files []map[string]any, answer string) {
	for _, block := range toolResultTagPattern.FindAllString(answer, -1) {
		var result struct {
			Name   string          `json:"name"`
			Result json.RawMessage `json:"result"`
		}
		content := block[strings.Index(block, ">")+1 : strings.LastIndex(block, "</")]
		if json.Unmarshal([]byte(content), &result) != nil || result.Name != "vision_extractor" {
			continue
		}
		var encoded string
		if json.Unmarshal(result.Result, &encoded) == nil {
			result.Result = []byte(encoded)
		}
		var description struct {
			Description string `json:"description"`
			URL         string `json:"url"`
		}
		if json.Unmarshal(result.Result, &description) != nil || description.Description == "" {
			continue
		}
		for _, file := range files {
			name := file["name"].(string)
			if name == description.URL || path.Base(name) == path.Base(description.URL) {
				file["description"], file["description_status"] = openingText(description.Description), "available"
			}
		}
	}
}

func loadOpeningSnapshot(db *gorm.DB, conv orm.Conversation, ignoredHistoryIDs ...string) (openingSnapshot, error) {
	var snapshot openingSnapshot
	ignored := make(map[string]struct{}, len(ignoredHistoryIDs))
	for _, id := range ignoredHistoryIDs {
		ignored[id] = struct{}{}
	}
	rows, err := db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", conv.ID).Order("seq ASC, create_time ASC, id ASC").Rows()
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	messages := []map[string]any{}
	files := []map[string]any{}
	evidence := []openingEvidenceRow{}
	needsSource := false
	for rows.Next() {
		var row orm.ChatHistory
		if err := db.ScanRows(rows, &row); err != nil {
			return snapshot, err
		}
		if row.RunStatus == "generating" || row.RunStatus == "running" {
			snapshot.Active = true
			break
		}
		text := strings.TrimSpace(openingText(displayChatHistoryContent(row.RawContent)))
		attachments := openingAttachments(row.Ext)
		openingAttachmentDescriptions(attachments, row.Result)
		if _, skip := ignored[row.ID]; skip {
			snapshot.IDs = append(snapshot.IDs, row.ID)
			evidence = append(evidence, openingEvidenceRow{row.ID, row.RawContent, row.Result, row.Ext})
			continue
		}
		if len(snapshot.IDs) >= maxOpeningScannedTurns {
			break
		}
		if snapshot.DefaultTitle == "" {
			snapshot.DefaultTitle = GetDefaultDisplayName(conv.ID, []map[string]any{{"text": text}})
		}
		if openingGreeting(text) && len(attachments) == 0 {
			continue
		}
		snapshot.Turns++
		needsSource = needsSource || openingSourceReference.MatchString(text)
		snapshot.IDs = append(snapshot.IDs, row.ID)
		evidence = append(evidence, openingEvidenceRow{row.ID, row.RawContent, row.Result, row.Ext})
		messages = append(messages, map[string]any{"role": "user", "content": text})
		files = append(files, attachments...)
		answer := openingClarification(row.Result)
		// Short clarifying replies are evidence; full answers and tool output are not.
		if answer != "" {
			messages = append(messages, map[string]any{"role": "assistant", "content": answer})
		}
		if snapshot.Turns == 3 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return snapshot, err
	}
	data := map[string]any{"attachments": files}
	if validChildConversation(conv) && needsSource {
		data["source_selected_text"] = openingText(conv.SourceSelectedText)
		var source conversationSourceContextSnapshot
		if json.Unmarshal(conv.SourceContext, &source) == nil {
			contextMessages := []map[string]any{}
			for _, message := range source.Messages {
				role, _ := message["role"].(string)
				content, _ := message["content"].(string)
				if role == "user" || role == "assistant" {
					contextMessages = append(contextMessages, map[string]any{"role": role, "content": openingText(content)})
				}
			}
			data["source_context"] = contextMessages
		}
	}
	snapshot.Input, _ = json.Marshal(map[string]any{"messages": messages, "data": data})
	snapshot.Hash = openingHash(snapshot.Input)
	snapshot.Evidence = openingHash([]any{openingEvidenceRows(evidence), conv.SourceContext, conv.SourceSelectedText})
	return snapshot, nil
}
