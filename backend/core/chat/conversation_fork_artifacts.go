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
	"lazymind/core/artifact"
	"lazymind/core/common/orm"
	"lazymind/core/subagent"
)

const maxForkArtifactBytes int64 = 64 << 20

// Artifact values are copied into ordinary download artifacts, without the
// task/workflow/Writer session that produced them.
type forkArtifactSnapshot struct {
	SourceID    string
	RevisionID  string
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
	historyOrder := map[string]int{}
	for index, h := range histories {
		ids = append(ids, h.ID)
		cutoffs[h.ID] = h.UpdateTime
		historyOrder[h.ID] = index
	}
	var direct []orm.ConversationArtifact
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id = ? AND create_user_id = ? AND history_id IN ?", c.ID, c.CreateUserID, ids).
		Where("created_at <= (SELECT update_time FROM chat_histories WHERE id = conversation_artifacts.history_id)").Order("id").Limit(201).Find(&direct).Error; err != nil {
		return nil, err
	}
	out := []forkArtifactSnapshot{}
	deliveries := map[string]artifact.PublishedProjection{}
	seenDeliveries := map[string]bool{}
	if artifact.Enabled() {
		rows, err := artifact.New(db).ConversationDeliveries(ctx, c.CreateUserID, c.ID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			deliveries[row.HistoryID+"/"+row.LegacyID] = row
		}
	}
	appendValue := func(id, historyID, filename, contentType string, raw json.RawMessage, caption *string, workspace string) error {
		revisionID := ""
		if receipt, ok := deliveries[historyID+"/"+id]; ok {
			seenDeliveries[historyID+"/"+id] = true
			revisionID, filename, contentType, caption = receipt.RevisionID, receipt.Filename, receipt.ContentType, receipt.Caption
			raw = receipt.InlineJSON
			if receipt.SnapshotPath != "" {
				raw, _ = json.Marshal(map[string]any{"path": receipt.SnapshotPath, "filename": filename})
				workspace = filepath.Dir(receipt.SnapshotPath)
			}
		}
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
			out = append(out, forkArtifactSnapshot{SourceID: id, RevisionID: revisionID, HistoryID: historyID, Filename: filename, ContentType: contentType, Value: encoded, Caption: caption})
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
			item := forkArtifactSnapshot{SourceID: id, RevisionID: revisionID, HistoryID: historyID, Filename: name, ContentType: "file", Caption: caption, Path: path}
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
	// A reused legacy ID keeps its original history_id. Later immutable
	// deliveries must still be included when the fork contains those turns.
	for key, receipt := range deliveries {
		cutoff, included := cutoffs[receipt.HistoryID]
		if seenDeliveries[key] || !included || receipt.CreatedAt.After(cutoff) {
			continue
		}
		if receipt.ProducerType == artifact.ProducerSubAgent {
			continue
		} // task visibility was checked above
		if err := appendValue(receipt.LegacyID, receipt.HistoryID, receipt.Filename, receipt.ContentType, receipt.InlineJSON, receipt.Caption, ""); err != nil {
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
		if out[i].HistoryID != out[j].HistoryID {
			return historyOrder[out[i].HistoryID] < historyOrder[out[j].HistoryID]
		}
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
	replacements := make([]map[string]string, len(copied))
	for i := range replacements {
		replacements[i] = map[string]string{}
	}
	remember := func(a forkArtifactSnapshot, from, to string) {
		if from == "" {
			return
		}
		eligible := a.RevisionID == ""
		for i := range source {
			if source[i].ID == a.HistoryID {
				eligible = true
			}
			if eligible {
				replacements[i][from] = to
			}
		}
	}
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
			remember(a, a.Path, destination)
		}
		remember(a, a.SourceID, row.ID)
		out = append(out, row)
	}
	// Resolve each history against its latest preceding delivery, then replace in
	// one pass so reused source IDs cannot be consumed by an earlier revision.
	for i, mapping := range replacements {
		keys := make([]string, 0, len(mapping))
		for key := range mapping {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(a, b int) bool { return len(keys[a]) > len(keys[b]) })
		pairs := make([]string, 0, len(keys)*2)
		for _, key := range keys {
			pairs = append(pairs, key, mapping[key])
		}
		copied[i].Result = strings.NewReplacer(pairs...).Replace(copied[i].Result)
	}
	return out, nil
}

func bindForkArtifactLineage(ctx context.Context, db *gorm.DB, userID, childConversationID string, snapshots []forkArtifactSnapshot, copies []orm.ConversationArtifact) error {
	if db == nil || !artifact.Enabled() {
		return nil
	}
	svc := artifact.InTransaction(db)
	for i, snap := range snapshots {
		if snap.Unavailable {
			continue
		}
		childID := ""
		if i < len(copies) {
			childID = copies[i].ID
		}
		var err error
		if snap.RevisionID != "" && i < len(copies) {
			err = artifact.BindForkRevision(ctx, svc, userID, snap.RevisionID, childConversationID, childID, copies[i].HistoryID)
		} else {
			err = artifact.BindForkConversation(ctx, svc, userID, snap.SourceID, childConversationID, childID)
			if err == nil && i < len(copies) {
				binding, lookupErr := svc.FindLatestLegacyBinding(ctx, artifact.ScopeLegacyRow, childID)
				if lookupErr == nil {
					err = svc.BindRevision(ctx, userID, artifact.BindingSpec{ScopeType: artifact.ScopeHistory, ScopeID: copies[i].HistoryID, Role: artifact.RoleOutput, RevisionID: binding.RevisionID}, binding.ArtifactID)
				} else if !errors.Is(lookupErr, artifact.ErrNotFound) {
					err = lookupErr
				}
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
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
