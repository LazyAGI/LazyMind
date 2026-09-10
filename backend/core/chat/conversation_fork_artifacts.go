package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/subagent"
)

const maxForkArtifactBytes int64 = 64 << 20

// Artifact values are copied into ordinary download artifacts, without the
// task/workflow/Writer session that produced them.
type forkArtifactSnapshot struct {
	SourceID    string
	HistoryID   string
	Filename    string
	ContentType string
	Value       json.RawMessage
	Caption     *string
	Path        string
	Size        int64
	ModifiedAt  time.Time
	Unavailable bool
}

func loadForkArtifacts(ctx context.Context, db *gorm.DB, c orm.Conversation, histories []orm.ChatHistory) ([]forkArtifactSnapshot, error) {
	ids := make([]string, 0, len(histories))
	cutoffs := map[string]time.Time{}
	for _, h := range histories {
		ids = append(ids, h.ID)
		cutoffs[h.ID] = h.UpdateTime
	}
	var direct []orm.ConversationArtifact
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id = ? AND create_user_id = ? AND history_id IN ?", c.ID, c.CreateUserID, ids).
		Where("created_at <= (SELECT update_time FROM chat_histories WHERE id = conversation_artifacts.history_id)").Order("id").Limit(201).Find(&direct).Error; err != nil {
		return nil, err
	}
	out := []forkArtifactSnapshot{}
	appendValue := func(id, historyID, filename, contentType string, raw json.RawMessage, caption *string, workspace string) error {
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil {
			return forkFail("CONFIG_UNSUPPORTED")
		}
		if contentType == "text" || contentType == "json" {
			key := "text"
			if contentType == "json" {
				key = "data"
			}
			if value[key] == nil {
				return forkFail("CONFIG_UNSUPPORTED")
			}
			encoded, _ := json.Marshal(map[string]any{key: value[key]})
			out = append(out, forkArtifactSnapshot{SourceID: id, HistoryID: historyID, Filename: filename, ContentType: contentType, Value: encoded, Caption: caption})
			return nil
		}
		resolved := subagent.ResolveArtifactSnapshotPaths(raw, workspace)
		if json.Unmarshal(resolved, &value) != nil {
			return forkFail("CONFIG_UNSUPPORTED")
		}
		paths := []string{}
		if contentType == "file_list" {
			paths = stringSliceFromAny(value["paths"])
		} else if contentType == "file" || contentType == "image" || strings.HasPrefix(contentType, "image/") {
			path, _ := value["path"].(string)
			paths = append(paths, path)
		} else {
			return forkFail("CONFIG_UNSUPPORTED")
		}
		if len(paths) == 0 {
			paths = []string{""}
		}
		for _, path := range paths {
			name := filename
			if !validArtifactFilename(name) {
				name = filepath.Base(path)
			}
			if !validArtifactFilename(name) {
				name = "artifact"
			}
			item := forkArtifactSnapshot{SourceID: id, HistoryID: historyID, Filename: name, ContentType: "file", Caption: caption, Path: path}
			if path == "" || !filepath.IsAbs(path) || strings.HasPrefix(path, "/static-files/") {
				item.Unavailable = true
			} else {
				info, err := os.Stat(path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err != nil || !info.Mode().IsRegular() {
					item.Unavailable = true
				} else {
					item.Size = info.Size()
					item.ModifiedAt = info.ModTime()
				}
			}
			out = append(out, item)
		}
		return nil
	}
	for _, a := range direct {
		if a.CreatedAt.After(cutoffs[a.HistoryID]) {
			continue
		}
		if err := appendValue(a.ID, a.HistoryID, a.Filename, a.ContentType, a.Value, a.Caption, conversationArtifactFileRoot(c.CreateUserID, c.ID, a.ID)); err != nil {
			return nil, err
		}
	}
	// Only artifacts that had reached the selected history by its terminal write
	// belong to that prefix; a later background result is not pulled backwards.
	records, err := subagent.ListArtifactsByConversationForUser(ctx, db, c.ID, c.CreateUserID)
	if err != nil {
		return nil, err
	}
	for _, a := range records {
		cutoff, included := cutoffs[a.TriggerHistoryID]
		if !included || a.CreatedAt.After(cutoff) {
			continue
		}
		if err := appendValue(a.ArtifactID, a.TriggerHistoryID, a.Slot, a.ContentType, a.Value, a.Caption, a.WorkspacePath); err != nil {
			return nil, err
		}
	}
	if len(out) > 200 {
		return nil, forkFail("FORK_TOO_LARGE")
	}
	var size int64
	for _, a := range out {
		size += a.Size + int64(len(a.Value))
	}
	if size > maxForkArtifactBytes {
		return nil, forkFail("FORK_TOO_LARGE")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SourceID == out[j].SourceID {
			return out[i].Path < out[j].Path
		}
		return out[i].SourceID < out[j].SourceID
	})
	return out, nil
}

func forkSnapshotRevision(histories []orm.ChatHistory, artifacts []forkArtifactSnapshot) (string, error) {
	historyRevision, err := forkPrefixRevision(histories)
	if err != nil || len(artifacts) == 0 {
		return historyRevision, err
	}
	return forkDigest(struct {
		History   string
		Artifacts []forkArtifactSnapshot
	}{historyRevision, artifacts})
}

func prepareForkArtifactCopies(userID, conversationID string, source, copied []orm.ChatHistory, artifacts []forkArtifactSnapshot) ([]orm.ConversationArtifact, error) {
	ids := map[string]string{}
	cutoffs := map[string]time.Time{}
	for i := range source {
		ids[source[i].ID] = copied[i].ID
		cutoffs[source[i].ID] = copied[i].UpdateTime
	}
	out := make([]orm.ConversationArtifact, 0, len(artifacts))
	for _, a := range artifacts {
		row := orm.ConversationArtifact{ID: newConversationID(), ConversationID: conversationID, HistoryID: ids[a.HistoryID], Filename: a.Filename, Slot: a.Filename, ContentType: a.ContentType, Value: a.Value, Caption: a.Caption, CreateUserID: userID, CreatedAt: cutoffs[a.HistoryID]}
		if a.Unavailable {
			row.ContentType = "text"
			row.Value = json.RawMessage(`{"text":"[Attachment unavailable]"}`)
		} else if a.ContentType == "file" {
			destination := filepath.Join(conversationArtifactFileRoot(userID, conversationID, row.ID), a.Filename)
			if err := copyForkArtifactFile(a, destination); err != nil {
				return nil, err
			}
			row.Value, _ = json.Marshal(map[string]any{"filename": a.Filename, "path": destination, "size": a.Size})
			for i := range copied {
				copied[i].Result = strings.ReplaceAll(copied[i].Result, a.Path, destination)
			}
		}
		for i := range copied {
			copied[i].Result = strings.ReplaceAll(copied[i].Result, a.SourceID, row.ID)
		}
		out = append(out, row)
	}
	return out, nil
}

func copyForkArtifactFile(a forkArtifactSnapshot, destination string) error {
	input, err := os.Open(a.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return forkFail("SOURCE_CHANGED")
		}
		return err
	}
	defer input.Close()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	if before.Size() != a.Size || !before.ModTime().Equal(a.ModifiedAt) {
		return forkFail("SOURCE_CHANGED")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxForkArtifactBytes+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if written != a.Size || after.Size() != a.Size || !after.ModTime().Equal(a.ModifiedAt) {
		return forkFail("SOURCE_CHANGED")
	}
	return nil
}
