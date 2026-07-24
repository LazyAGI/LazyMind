package chat

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	exportFileTypeUnspecified = "EXPORT_FILE_TYPE_UNSPECIFIED"
	exportFileTypeXLSX        = "EXPORT_FILE_TYPE_XLSX"
	exportFileTypeZIP         = "EXPORT_FILE_TYPE_ZIP"
)

type ExportConversationsRequest struct {
	ConversationIDs []string `json:"conversation_ids,omitempty"`
	FileTypes       []string `json:"file_types"`
	Keyword         string   `json:"keyword,omitempty"`
	StartTime       string   `json:"start_time,omitempty"`
	EndTime         string   `json:"end_time,omitempty"`
	FeedBack        *int     `json:"feed_back,omitempty"`
	CreateUserNames []string `json:"create_user_names,omitempty"`
}

type ExportConversationsResponse struct {
	Uris []string `json:"uris,omitempty"`
}

type exportConversationBundle struct {
	Conversation orm.Conversation  `json:"conversation"`
	Histories    []orm.ChatHistory `json:"histories"`
}

type exportFileMeta struct {
	Path        string
	FileName    string
	ContentType string
	UserID      string
	ExpireAt    time.Time
}

var (
	exportFilesMu sync.Mutex
	exportFiles   = map[string]exportFileMeta{}
)

func ExportConversations(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ExportConversationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	fileTypes := normalizeExportFileTypes(req.FileTypes)
	if len(fileTypes) == 0 {
		fileTypes = []string{exportFileTypeXLSX}
	}

	startAt, err := parseOptionalTime(req.StartTime)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid start_time", err), http.StatusBadRequest)
		return
	}
	endAt, err := parseOptionalTime(req.EndTime)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid end_time", err), http.StatusBadRequest)
		return
	}
	if !startAt.IsZero() && !endAt.IsZero() && endAt.Before(startAt) {
		common.ReplyErr(w, "end_time must be greater than or equal to start_time", http.StatusBadRequest)
		return
	}

	conversations, historiesByConvID, err := loadConversationsForExport(r, userID, req, startAt, endAt)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query conversations failed", err), http.StatusInternalServerError)
		return
	}
	if len(conversations) == 0 {
		common.ReplyJSON(w, ExportConversationsResponse{Uris: []string{}})
		return
	}

	exportFilesMu.Lock()
	purgeExpiredExportFilesLocked(time.Now().UTC())
	exportFilesMu.Unlock()

	bundles := make([]exportConversationBundle, 0, len(conversations))
	for _, conv := range conversations {
		bundles = append(bundles, exportConversationBundle{
			Conversation: conv,
			Histories:    historiesByConvID[conv.ID],
		})
	}

	uris := make([]string, 0, len(fileTypes))
	for _, ft := range fileTypes {
		meta, err := buildExportFile(bundles, userID, ft)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "export conversations failed", err), http.StatusInternalServerError)
			return
		}
		token := newID("export_")
		exportFilesMu.Lock()
		exportFiles[token] = meta
		exportFilesMu.Unlock()
		uris = append(uris, "/api/v1/conversation:export/files/"+token)
	}
	common.ReplyJSON(w, ExportConversationsResponse{Uris: uris})
}

func DownloadExportConversationFile(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	fileID := strings.TrimSpace(common.PathVar(r, "file_id"))
	if fileID == "" {
		common.ReplyErr(w, "missing file_id", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	exportFilesMu.Lock()
	purgeExpiredExportFilesLocked(now)
	meta, ok := exportFiles[fileID]
	exportFilesMu.Unlock()
	if !ok {
		common.ReplyErr(w, "export file not found", http.StatusNotFound)
		return
	}
	if meta.UserID != userID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(common.ForbiddenBody))
		return
	}

	f, err := os.Open(meta.Path)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "export file not found", err), http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", meta.FileName))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func loadConversationsForExport(
	r *http.Request,
	userID string,
	req ExportConversationsRequest,
	startAt time.Time,
	endAt time.Time,
) ([]orm.Conversation, map[string][]orm.ChatHistory, error) {
	db := store.DB().WithContext(r.Context())
	q := db.Model(&orm.Conversation{}).Where("create_user_id = ?", userID)
	if len(req.ConversationIDs) > 0 {
		ids := make([]string, 0, len(req.ConversationIDs))
		for _, id := range req.ConversationIDs {
			if s := strings.TrimSpace(id); s != "" {
				ids = append(ids, s)
			}
		}
		if len(ids) == 0 {
			return []orm.Conversation{}, map[string][]orm.ChatHistory{}, nil
		}
		q = q.Where("id IN ?", ids)
	}
	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		q = q.Where("display_name LIKE ?", "%"+kw+"%")
	}
	if len(req.CreateUserNames) > 0 {
		names := make([]string, 0, len(req.CreateUserNames))
		for _, n := range req.CreateUserNames {
			if s := strings.TrimSpace(n); s != "" {
				names = append(names, s)
			}
		}
		if len(names) > 0 {
			q = q.Where("create_user_name IN ?", names)
		}
	}
	if !startAt.IsZero() {
		q = q.Where("created_at >= ?", startAt)
	}
	if !endAt.IsZero() {
		q = q.Where("created_at <= ?", endAt)
	}
	var conversations []orm.Conversation
	if err := q.Order("updated_at DESC").Find(&conversations).Error; err != nil {
		return nil, nil, err
	}
	if len(conversations) == 0 {
		return []orm.Conversation{}, map[string][]orm.ChatHistory{}, nil
	}

	convIDs := make([]string, 0, len(conversations))
	for _, c := range conversations {
		convIDs = append(convIDs, c.ID)
	}
	hq := db.Model(&orm.ChatHistory{}).Where("conversation_id IN ?", convIDs)
	if req.FeedBack != nil && *req.FeedBack > 0 {
		hq = hq.Where("feed_back = ?", *req.FeedBack)
	}
	var histories []orm.ChatHistory
	if err := hq.Order("conversation_id ASC, seq ASC, create_time ASC").Find(&histories).Error; err != nil {
		return nil, nil, err
	}
	byConv := make(map[string][]orm.ChatHistory, len(conversations))
	for _, h := range histories {
		byConv[h.ConversationID] = append(byConv[h.ConversationID], h)
	}
	return conversations, byConv, nil
}

func buildExportFile(bundles []exportConversationBundle, userID string, fileType string) (exportFileMeta, error) {
	now := time.Now().UTC()
	switch fileType {
	case exportFileTypeZIP:
		data, err := buildConversationsZIP(bundles)
		if err != nil {
			return exportFileMeta{}, err
		}
		path, err := writeTempExportFile("conversations-export-*.zip", data)
		if err != nil {
			return exportFileMeta{}, err
		}
		return exportFileMeta{
			Path:        path,
			FileName:    fmt.Sprintf("conversations_%s.zip", now.Format("20060102_150405")),
			ContentType: "application/zip",
			UserID:      userID,
			ExpireAt:    now.Add(2 * time.Hour),
		}, nil
	default:
		data, err := buildConversationsCSV(bundles)
		if err != nil {
			return exportFileMeta{}, err
		}
		path, err := writeTempExportFile("conversations-export-*.csv", data)
		if err != nil {
			return exportFileMeta{}, err
		}
		return exportFileMeta{
			Path:        path,
			FileName:    fmt.Sprintf("conversations_%s.csv", now.Format("20060102_150405")),
			ContentType: "text/csv; charset=utf-8",
			UserID:      userID,
			ExpireAt:    now.Add(2 * time.Hour),
		}, nil
	}
}

func buildConversationsCSV(bundles []exportConversationBundle) ([]byte, error) {
	var buf bytes.Buffer
	// Add UTF-8 BOM so spreadsheet apps open Chinese text correctly.
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{
		"conversation_id", "display_name", "create_user_name", "conversation_create_time", "conversation_update_time",
		"seq", "query", "result", "feed_back", "reason", "expected_answer", "history_create_time",
	})
	for _, bundle := range bundles {
		conv := bundle.Conversation
		if len(bundle.Histories) == 0 {
			_ = w.Write([]string{
				conv.ID,
				conv.DisplayName,
				conv.CreateUserName,
				conv.CreatedAt.UTC().Format(time.RFC3339),
				conv.UpdatedAt.UTC().Format(time.RFC3339),
				"", "", "", "", "", "", "",
			})
			continue
		}
		for _, h := range bundle.Histories {
			_ = w.Write([]string{
				conv.ID,
				conv.DisplayName,
				conv.CreateUserName,
				conv.CreatedAt.UTC().Format(time.RFC3339),
				conv.UpdatedAt.UTC().Format(time.RFC3339),
				fmt.Sprintf("%d", h.Seq),
				h.RawContent,
				h.Result,
				fmt.Sprintf("%d", h.FeedBack),
				h.Reason,
				h.ExpectedAnswer,
				h.CreateTime.UTC().Format(time.RFC3339),
			})
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildConversationsZIP(bundles []exportConversationBundle) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	jsonFile, err := zw.Create("conversations.json")
	if err != nil {
		return nil, err
	}
	payload, _ := json.MarshalIndent(map[string]any{"conversations": bundles}, "", "  ")
	if _, err := jsonFile.Write(payload); err != nil {
		return nil, err
	}

	csvData, err := buildConversationsCSV(bundles)
	if err != nil {
		return nil, err
	}
	csvFile, err := zw.Create("conversations.csv")
	if err != nil {
		return nil, err
	}
	if _, err := csvFile.Write(csvData); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeTempExportFile(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func purgeExpiredExportFilesLocked(now time.Time) {
	for key, meta := range exportFiles {
		if now.Before(meta.ExpireAt) {
			continue
		}
		_ = os.Remove(meta.Path)
		delete(exportFiles, key)
	}
}

func parseOptionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time")
}

func normalizeExportFileTypes(fileTypes []string) []string {
	if len(fileTypes) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fileTypes))
	for _, ft := range fileTypes {
		v := strings.TrimSpace(ft)
		if v == "" || v == exportFileTypeUnspecified {
			continue
		}
		if v != exportFileTypeXLSX && v != exportFileTypeZIP {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
