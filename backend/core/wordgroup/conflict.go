package wordgroup

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"

	"gorm.io/gorm"
)

// DeleteWordGroupConflictResponse mirrors DeleteWordGroupResponse for symmetry.
type DeleteWordGroupConflictResponse struct {
	ID          string `json:"id"`
	DeletedRows int64  `json:"deleted_rows"`
}

// WordGroupConflictResponse is one item returned by GET /word_group_conflict.
// group_ids is parsed back from the stored JSON-serialized string.
type WordGroupConflictResponse struct {
	ID          string    `json:"id"`
	Reason      string    `json:"reason"`
	Word        string    `json:"word"`
	Description string    `json:"description"`
	GroupIDs    []string  `json:"group_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListWordGroupConflictsResponse mirrors the existing word-group list pagination shape.
type ListWordGroupConflictsResponse struct {
	Items         []WordGroupConflictResponse `json:"items"`
	TotalSize     int32                       `json:"total_size"`
	NextPageToken string                      `json:"next_page_token"`
}

// AddWordGroupConflictToGroupsRequest adds a conflict word into one or more selected groups.
type AddWordGroupConflictToGroupsRequest struct {
	ID       string   `json:"id"`
	Word     string   `json:"word"`
	GroupIDs []string `json:"group_ids"`
}

// AddWordGroupConflictToGroupsResponse reports per-group insertion status.
type AddWordGroupConflictToGroupsResponse struct {
	Word                string   `json:"word"`
	GroupIDs            []string `json:"group_ids"`
	AddedGroups         []string `json:"added_groups"`
	SkippedGroups       []string `json:"skipped_groups"`
	DeletedConflictRows int64    `json:"deleted_conflict_rows"`
}

// CreateWordGroupFromConflictRequest creates a new word group from conflict resolution fields,
// optionally adds the conflict word into existing groups, then resolves the conflict row.
type CreateWordGroupFromConflictRequest struct {
	ID          string   `json:"id"`
	Word        string   `json:"word"`
	GroupIDs    []string `json:"group_ids"`
	Term        string   `json:"term"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

// CreateWordGroupFromConflictResponse is the created group plus optional add-to-existing-groups stats.
type CreateWordGroupFromConflictResponse struct {
	CreateWordGroupResponse
	Word                string   `json:"word"`
	GroupIDs            []string `json:"group_ids"`
	AddedGroups         []string `json:"added_groups"`
	SkippedGroups       []string `json:"skipped_groups"`
	DeletedConflictRows int64    `json:"deleted_conflict_rows"`
}

// ListWordGroupConflicts returns the requesting user's pending conflicts ordered by updated_at DESC.
// Hits idx_word_group_conflict_user_updated (partial composite index).
func ListWordGroupConflicts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	q := r.URL.Query()
	pageToken := strings.TrimSpace(q.Get("page_token"))
	pageSizeStr := strings.TrimSpace(q.Get("page_size"))

	pageSize := 20
	if pageSizeStr != "" {
		if v, err := strconv.Atoi(pageSizeStr); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if pageToken != "" {
		if v, err := parseListPageToken(pageToken); err == nil && v >= 0 {
			offset = v
		}
	}

	db := store.DB()
	scope := db.Model(&orm.WordGroupConflict{}).
		Where("create_user_id = ? AND deleted_at IS NULL", userID)

	var total int64
	if err := scope.Count(&total).Error; err != nil {
		log.Logger.Error().Err(err).Msg("count word_group_conflicts failed")
		common.ReplyErr(w, "list word group conflicts failed", http.StatusInternalServerError)
		return
	}

	var rows []orm.WordGroupConflict
	if err := scope.Order("updated_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&rows).Error; err != nil {
		log.Logger.Error().Err(err).Msg("list word_group_conflicts failed")
		common.ReplyErr(w, "list word group conflicts failed", http.StatusInternalServerError)
		return
	}

	items := make([]WordGroupConflictResponse, 0, len(rows))
	for i := range rows {
		groupIDs, _ := parseJSONStringSliceField(rows[i].GroupIDs)
		if groupIDs == nil {
			groupIDs = []string{}
		}
		items = append(items, WordGroupConflictResponse{
			ID:          rows[i].ID,
			Reason:      rows[i].Reason,
			Word:        rows[i].Word,
			Description: rows[i].Description,
			GroupIDs:    groupIDs,
			CreatedAt:   rows[i].CreatedAt,
			UpdatedAt:   rows[i].UpdatedAt,
		})
	}

	end := offset + len(items)
	nextToken := ""
	if end < int(total) {
		nextToken = encodeListPageToken(end, pageSize, int(total))
	}

	common.ReplyOK(w, ListWordGroupConflictsResponse{
		Items:         items,
		TotalSize:     int32(total),
		NextPageToken: nextToken,
	})
}

// DeleteWordGroupConflict soft-deletes a single conflict row owned by the request user.
// Hits the row by primary key (id) scoped to create_user_id; returns 404 if not found.
func DeleteWordGroupConflict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(common.PathVar(r, "id"))
	if id == "" {
		common.ReplyErr(w, "missing id", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	res := store.DB().Model(&orm.WordGroupConflict{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", id, userID).
		Updates(map[string]any{
			"deleted_at": now,
			"updated_at": now,
		})
	if err := res.Error; err != nil {
		log.Logger.Error().Err(err).Str("id", id).Msg("delete word_group_conflict failed")
		common.ReplyErr(w, "delete word group conflict failed", http.StatusInternalServerError)
		return
	}
	if res.RowsAffected == 0 {
		common.ReplyErr(w, "word group conflict not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, DeleteWordGroupConflictResponse{ID: id, DeletedRows: res.RowsAffected})
}

// AddWordGroupConflictToGroups inserts the conflict word as alias into selected groups.
// Existing words in a target group are skipped and reported in skipped_groups.
func AddWordGroupConflictToGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var body AddWordGroupConflictToGroupsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	words := normalizeAliases([]string{body.Word})
	if len(words) == 0 {
		common.ReplyErr(w, "word is required", http.StatusBadRequest)
		return
	}
	word := words[0]
	conflictID := strings.TrimSpace(body.ID)
	if conflictID == "" {
		common.ReplyErr(w, "id is required", http.StatusBadRequest)
		return
	}
	groupIDs := dedupeGroupIDsPreserveOrder(body.GroupIDs)
	if len(groupIDs) == 0 {
		common.ReplyErr(w, "group_ids is required", http.StatusBadRequest)
		return
	}

	addedGroups := make([]string, 0, len(groupIDs))
	skippedGroups := make([]string, 0)
	now := time.Now().UTC()
	var deletedConflictRows int64

	err := store.DB().Transaction(func(tx *gorm.DB) error {
		var err error
		addedGroups, skippedGroups, err = addConflictWordToGroupsInTx(tx, userID, word, groupIDs, now)
		if err != nil {
			return err
		}

		// After the word has been processed for selected groups, resolve (soft-delete)
		// the target conflict row for this user by id.
		res := tx.Model(&orm.WordGroupConflict{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conflictID, userID).
			Updates(map[string]any{
				"deleted_at": now,
				"updated_at": now,
			})
		if err := res.Error; err != nil {
			return err
		}
		deletedConflictRows = res.RowsAffected
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "target group not found", http.StatusNotFound)
			return
		}
		log.Logger.Error().Err(err).Str("word", word).Str("create_user_id", userID).Msg("add conflict word to groups failed")
		common.ReplyErr(w, "add conflict word to groups failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, AddWordGroupConflictToGroupsResponse{
		Word:                word,
		GroupIDs:            groupIDs,
		AddedGroups:         addedGroups,
		SkippedGroups:       skippedGroups,
		DeletedConflictRows: deletedConflictRows,
	})
}

// CreateWordGroupFromConflict creates a new word group, optionally adds the conflict word into
// existing groups from group_ids, then soft-deletes the conflict row.
func CreateWordGroupFromConflict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	userName := store.UserName(r)

	var body CreateWordGroupFromConflictRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	term := strings.TrimSpace(body.Term)
	desc := strings.TrimSpace(body.Description)
	aliases := normalizeAliases(body.Aliases)
	conflictID := strings.TrimSpace(body.ID)
	words := normalizeAliases([]string{body.Word})
	if len(words) == 0 {
		common.ReplyErr(w, "word is required", http.StatusBadRequest)
		return
	}
	word := words[0]
	if conflictID == "" {
		common.ReplyErr(w, "id is required", http.StatusBadRequest)
		return
	}
	if term == "" {
		common.ReplyErr(w, "term is required", http.StatusBadRequest)
		return
	}
	if msg := validateTermAndAliases(term, aliases); msg != "" {
		common.ReplyErr(w, msg, http.StatusBadRequest)
		return
	}
	if !aliasesContainWord(aliases, word) {
		common.ReplyErr(w, "aliases must contain word", http.StatusBadRequest)
		return
	}

	existingGroupIDs := dedupeGroupIDsPreserveOrder(body.GroupIDs)
	groupID := common.GenerateID()
	src := normalizeSource("")
	ref := ""
	now := time.Now().UTC()
	base := orm.WordBase{
		CreateUserID:   userID,
		CreateUserName: userName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	var termID string
	createdAliases := make([]CreatedAlias, 0, len(aliases))
	var addedGroups, skippedGroups []string
	var deletedConflictRows int64

	err := store.DB().Transaction(func(tx *gorm.DB) error {
		termID = common.GenerateID()
		termRow := orm.Word{
			ID:            termID,
			Word:          term,
			WordKind:      orm.WordKindTerm,
			GroupID:       groupID,
			Description:   desc,
			Source:        src,
			ReferenceInfo: ref,
			Locked:        false,
			WordBase:      base,
		}
		if err := tx.Create(&termRow).Error; err != nil {
			return err
		}
		for _, a := range aliases {
			aid := common.GenerateID()
			ar := orm.Word{
				ID:            aid,
				Word:          a,
				WordKind:      orm.WordKindAlias,
				GroupID:       groupID,
				Description:   desc,
				Source:        src,
				ReferenceInfo: ref,
				Locked:        false,
				WordBase:      base,
			}
			if err := tx.Create(&ar).Error; err != nil {
				return err
			}
			createdAliases = append(createdAliases, CreatedAlias{ID: aid, Word: a})
		}

		targetGroupIDs := make([]string, 0, len(existingGroupIDs))
		for _, gid := range existingGroupIDs {
			if gid != groupID {
				targetGroupIDs = append(targetGroupIDs, gid)
			}
		}
		if len(targetGroupIDs) > 0 {
			var err error
			addedGroups, skippedGroups, err = addConflictWordToGroupsInTx(tx, userID, word, targetGroupIDs, now)
			if err != nil {
				return err
			}
		}

		res := tx.Model(&orm.WordGroupConflict{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conflictID, userID).
			Updates(map[string]any{
				"deleted_at": now,
				"updated_at": now,
			})
		if err := res.Error; err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return errWordGroupConflictNotFound
		}
		deletedConflictRows = res.RowsAffected
		return nil
	})
	if errors.Is(err, errWordGroupConflictNotFound) {
		common.ReplyErr(w, "word group conflict not found", http.StatusNotFound)
		return
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "target group not found", http.StatusNotFound)
			return
		}
		log.Logger.Error().Err(err).Str("conflict_id", conflictID).Msg("create word group from conflict failed")
		common.ReplyErr(w, "create word group from conflict failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, CreateWordGroupFromConflictResponse{
		CreateWordGroupResponse: CreateWordGroupResponse{
			TermID:      termID,
			Term:        term,
			GroupID:     groupID,
			Aliases:     createdAliases,
			Description: desc,
			Source:      src,
			Reference:   ref,
			Lock:        false,
		},
		Word:                word,
		GroupIDs:            existingGroupIDs,
		AddedGroups:         addedGroups,
		SkippedGroups:       skippedGroups,
		DeletedConflictRows: deletedConflictRows,
	})
}

func addConflictWordToGroupsInTx(tx *gorm.DB, userID, word string, groupIDs []string, now time.Time) (added, skipped []string, err error) {
	for _, groupID := range groupIDs {
		var termRow orm.Word
		if err := tx.Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL",
			groupID, userID, orm.WordKindTerm).First(&termRow).Error; err != nil {
			return nil, nil, err
		}

		var count int64
		if err := tx.Model(&orm.Word{}).
			Where("group_id = ? AND create_user_id = ? AND word = ? AND deleted_at IS NULL", groupID, userID, word).
			Count(&count).Error; err != nil {
			return nil, nil, err
		}
		if count > 0 {
			skipped = append(skipped, groupID)
			continue
		}

		row := orm.Word{
			ID:            common.GenerateID(),
			Word:          word,
			WordKind:      orm.WordKindAlias,
			GroupID:       groupID,
			Description:   termRow.Description,
			Source:        termRow.Source,
			ReferenceInfo: termRow.ReferenceInfo,
			Locked:        termRow.Locked,
			WordBase: orm.WordBase{
				CreateUserID:   userID,
				CreateUserName: termRow.CreateUserName,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, nil, err
		}
		added = append(added, groupID)
	}
	return added, skipped, nil
}
