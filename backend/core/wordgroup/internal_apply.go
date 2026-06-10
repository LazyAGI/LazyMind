package wordgroup

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"

	"gorm.io/gorm"
)

const (
	wordGroupActionCreateNewGroup = "create_new_group"
	wordGroupActionAddToGroup     = "add_to_group"
	wordGroupActionConflict       = "conflict"

	emptyJSONArray = "[]"
)

var (
	errApplyUserID              = errors.New("user_id is required")
	errApplyWords               = errors.New("word is required")
	errApplyGroupIDsAddToGroup  = errors.New("group_ids is required for add_to_group")
	errApplyGroupIDsConflict    = errors.New("group_ids is required for conflict")
	errApplyInvalidAction       = errors.New("invalid action")
	errApplyInvalidGroupIDsJSON = errors.New("invalid group_ids JSON")
)

// ApplyWordGroupActionItem is one element of request body field action_list (algorithm → core contract).
type ApplyWordGroupActionItem struct {
	Reason      string   `json:"reason"`
	Words       []string `json:"words"`
	Description string   `json:"description"`
	GroupIDs    string   `json:"group_ids"` // JSON-serialized array, e.g. ["id1","id2"]
	UserID      string   `json:"user_id"`
	MessageIDs  string   `json:"message_ids"` // JSON-serialized array of message ids
	Action      string   `json:"action"`
}

// ApplyWordGroupActionRequest is the POST body for /inner/word_group:apply (algorithm → core contract).
type ApplyWordGroupActionRequest struct {
	ActionList []ApplyWordGroupActionItem `json:"action_list"`
}

// ApplyWordGroupActionResponse summarizes what happened for one item.
type ApplyWordGroupActionResponse struct {
	Action       string   `json:"action"`
	GroupIDs     []string `json:"group_ids,omitempty"`
	AddedWords   []string `json:"added_words,omitempty"`
	SkippedWords []string `json:"skipped_words,omitempty"`
	ConflictID   string   `json:"conflict_id,omitempty"`
}

// ApplyWordGroupActionBatchResponse is returned in APIResponse.Data for POST /inner/word_group:apply.
type ApplyWordGroupActionBatchResponse struct {
	Results []ApplyWordGroupActionResponse `json:"results"`
}

// preparedApplyActions buckets validated input by action; every word becomes one record.
//   - Creates / Conflicts hold table-ready rows.
//   - Adds holds (idx, user, group, word, ...) entries since term lookup is required at write time.
//   - Responses is pre-shaped per item; batch ops fill in per-item runtime fields (added/skipped).
type preparedApplyActions struct {
	Responses []ApplyWordGroupActionResponse
	Creates   []orm.Word
	Adds      []preparedAddRow
	Conflicts []orm.WordGroupConflict
}

// preparedAddRow is a single (item, group_id, word) intent for action add_to_group.
// MessageIDs is the raw JSON-serialized string from input, used verbatim as reference_info.
type preparedAddRow struct {
	Idx        int
	UserID     string
	GroupID    string
	Word       string
	MessageIDs string
}

// ApplyWordGroupAction handles internal word-group ingest requests from algorithm service.
// Request body JSON has key action_list (array of items). This endpoint does not read X-User-Id
// and uses user_id from each item.
func ApplyWordGroupAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ApplyWordGroupActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	items := req.ActionList
	if len(items) == 0 {
		common.ReplyErr(w, "action_list must be a non-empty array", http.StatusBadRequest)
		return
	}

	prepared, err := prepareApplyActions(items)
	if err != nil {
		replyApplyError(w, err)
		return
	}

	db := store.DB()
	if err := runCreateNewGroupBatch(db, prepared.Creates); err != nil {
		replyApplyError(w, err)
		return
	}
	if err := runAddToGroupBatch(db, prepared.Adds, prepared.Responses); err != nil {
		replyApplyError(w, err)
		return
	}
	if err := runConflictBatch(db, prepared.Conflicts); err != nil {
		replyApplyError(w, err)
		return
	}
	common.ReplyOK(w, ApplyWordGroupActionBatchResponse{Results: prepared.Responses})
}

// prepareApplyActions validates each item once and explodes it into per-word records,
// dispatching them into Creates / Adds / Conflicts according to action.
//
// group_ids and message_ids are treated as opaque JSON-serialized strings:
//   - conflict path:    written to the DB as-is, no parse / re-marshal.
//   - add_to_group:     parses group_ids inline because per-group SQL lookups need the slice.
//   - create_new_group: doesn't need group_ids; message_ids passes through to reference_info as raw JSON.
func prepareApplyActions(items []ApplyWordGroupActionItem) (preparedApplyActions, error) {
	prepared := preparedApplyActions{
		Responses: make([]ApplyWordGroupActionResponse, len(items)),
	}
	for i := range items {
		body := &items[i]

		userID := strings.TrimSpace(body.UserID)
		if userID == "" {
			return prepared, errApplyUserID
		}
		words := normalizeAliases(body.Words)
		if len(words) == 0 {
			return prepared, errApplyWords
		}
		groupIDsJSON := normalizeJSONArrayString(body.GroupIDs)
		messageIDsJSON := normalizeJSONArrayString(body.MessageIDs)

		action := strings.TrimSpace(body.Action)
		description := strings.TrimSpace(body.Description)
		reason := strings.TrimSpace(body.Reason)

		switch action {
		case wordGroupActionCreateNewGroup:
			groupID := common.GenerateID()
			ref := messageIDsJSON
			now := time.Now().UTC()
			base := orm.WordBase{
				CreateUserID:   userID,
				CreateUserName: "system",
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			for j, word := range words {
				kind := orm.WordKindAlias
				if j == 0 {
					kind = orm.WordKindTerm
				}
				prepared.Creates = append(prepared.Creates, orm.Word{
					ID:            common.GenerateID(),
					Word:          word,
					WordKind:      kind,
					GroupID:       groupID,
					Description:   description,
					Source:        "ai",
					ReferenceInfo: ref,
					WordBase:      base,
				})
			}
			prepared.Responses[i] = ApplyWordGroupActionResponse{
				Action:     wordGroupActionCreateNewGroup,
				GroupIDs:   []string{groupID},
				AddedWords: words,
			}

		case wordGroupActionAddToGroup:
			groupIDs, err := parseJSONStringSliceField(groupIDsJSON)
			if err != nil {
				return prepared, fmt.Errorf("%w: %v", errApplyInvalidGroupIDsJSON, err)
			}
			groupIDs = dedupeGroupIDsPreserveOrder(groupIDs)
			if len(groupIDs) == 0 {
				return prepared, errApplyGroupIDsAddToGroup
			}
			// Group-major order matches original semantics (and is friendly for the per-group lookup).
			for _, gid := range groupIDs {
				for _, word := range words {
					prepared.Adds = append(prepared.Adds, preparedAddRow{
						Idx:        i,
						UserID:     userID,
						GroupID:    gid,
						Word:       word,
						MessageIDs: messageIDsJSON,
					})
				}
			}
			prepared.Responses[i] = ApplyWordGroupActionResponse{
				Action:   wordGroupActionAddToGroup,
				GroupIDs: groupIDs,
			}

		case wordGroupActionConflict:
			if groupIDsJSON == emptyJSONArray {
				return prepared, errApplyGroupIDsConflict
			}
			now := time.Now().UTC()
			var firstID string
			for _, word := range words {
				rowID := common.GenerateID()
				if firstID == "" {
					firstID = rowID
				}
				prepared.Conflicts = append(prepared.Conflicts, orm.WordGroupConflict{
					ID:           rowID,
					Reason:       reason,
					Word:         word,
					Description:  description,
					GroupIDs:     groupIDsJSON,
					CreateUserID: userID,
					MessageIDs:   messageIDsJSON,
					CreatedAt:    now,
					UpdatedAt:    now,
				})
			}
			prepared.Responses[i] = ApplyWordGroupActionResponse{
				Action:     wordGroupActionConflict,
				ConflictID: firstID,
			}

		default:
			return prepared, errApplyInvalidAction
		}
	}
	return prepared, nil
}

// runCreateNewGroupBatch bulk-inserts pre-built words rows in a single transaction.
func runCreateNewGroupBatch(db *gorm.DB, rows []orm.Word) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.CreateInBatches(&rows, 100).Error
	})
}

// runConflictBatch bulk-inserts pre-built conflict rows in a single transaction.
func runConflictBatch(db *gorm.DB, rows []orm.WordGroupConflict) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.CreateInBatches(&rows, 100).Error
	})
}

// runAddToGroupBatch resolves per-(userID, groupID) term row + existing words exactly once,
// then iterates the per-word records in original order, populating responses[idx].
func runAddToGroupBatch(db *gorm.DB, items []preparedAddRow, responses []ApplyWordGroupActionResponse) error {
	if len(items) == 0 {
		return nil
	}

	type bucketKey struct {
		UserID  string
		GroupID string
	}
	buckets := make(map[bucketKey][]int)
	keyOrder := make([]bucketKey, 0)
	for i := range items {
		k := bucketKey{UserID: items[i].UserID, GroupID: items[i].GroupID}
		if _, ok := buckets[k]; !ok {
			keyOrder = append(keyOrder, k)
		}
		buckets[k] = append(buckets[k], i)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, k := range keyOrder {
			var termRow orm.Word
			if err := tx.Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL",
				k.GroupID, k.UserID, orm.WordKindTerm).First(&termRow).Error; err != nil {
				return err
			}

			var existingRows []orm.Word
			if err := tx.Where(
				"group_id = ? AND create_user_id = ? AND deleted_at IS NULL",
				k.GroupID,
				k.UserID,
			).Find(&existingRows).Error; err != nil {
				return err
			}
			existing := make(map[string]struct{}, len(existingRows))
			for j := range existingRows {
				existing[existingRows[j].Word] = struct{}{}
			}

			now := time.Now().UTC()
			base := orm.WordBase{
				CreateUserID:   k.UserID,
				CreateUserName: termRow.CreateUserName,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			for _, recIdx := range buckets[k] {
				rec := &items[recIdx]
				ref := rec.MessageIDs
				if _, dup := existing[rec.Word]; dup {
					responses[rec.Idx].SkippedWords = append(responses[rec.Idx].SkippedWords, rec.Word)
					continue
				}
				row := orm.Word{
					ID:            common.GenerateID(),
					Word:          rec.Word,
					WordKind:      orm.WordKindAlias,
					GroupID:       k.GroupID,
					Description:   termRow.Description,
					Source:        termRow.Source,
					ReferenceInfo: ref,
					Locked:        termRow.Locked,
					WordBase:      base,
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				existing[rec.Word] = struct{}{}
				responses[rec.Idx].AddedWords = append(responses[rec.Idx].AddedWords, rec.Word)
			}
		}
		return nil
	})
}

func replyApplyError(w http.ResponseWriter, err error) {
	log.Logger.Error().Err(err).Msg("apply word_group action failed")
	switch {
	case errors.Is(err, errApplyUserID):
		common.ReplyErr(w, errApplyUserID.Error(), http.StatusBadRequest)
	case errors.Is(err, errApplyWords):
		common.ReplyErr(w, errApplyWords.Error(), http.StatusBadRequest)
	case errors.Is(err, errApplyGroupIDsAddToGroup):
		common.ReplyErr(w, errApplyGroupIDsAddToGroup.Error(), http.StatusBadRequest)
	case errors.Is(err, errApplyGroupIDsConflict):
		common.ReplyErr(w, errApplyGroupIDsConflict.Error(), http.StatusBadRequest)
	case errors.Is(err, errApplyInvalidAction):
		common.ReplyErr(w, errApplyInvalidAction.Error(), http.StatusBadRequest)
	case errors.Is(err, errApplyInvalidGroupIDsJSON):
		common.ReplyErr(w, errApplyInvalidGroupIDsJSON.Error(), http.StatusBadRequest)
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "target group not found", http.StatusNotFound)
	default:
		common.ReplyErr(w, "apply word group action failed", http.StatusInternalServerError)
	}
}

// normalizeJSONArrayString trims whitespace and substitutes "[]" for empty input so the result
// is always a syntactically valid JSON array literal.
func normalizeJSONArrayString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return emptyJSONArray
	}
	return raw
}

// parseJSONStringSliceField parses a JSON array of strings from a serialized field (empty → nil slice).
func parseJSONStringSliceField(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}
