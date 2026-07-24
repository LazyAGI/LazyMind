package wordgroup

import (
	"encoding/json"
	"errors"
	"fmt"
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

// CreateWordGroupRequest is the JSON body for POST /word_group.
// Submits one term plus optional aliases in a single request.
type CreateWordGroupRequest struct {
	Term        string   `json:"term"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	Lock        bool     `json:"lock"` // protected
	Conflict    bool     `json:"conflict"`
	ID          string   `json:"id"`
}

// UpdateWordGroupRequest is the JSON body for POST /word_group:update.
type UpdateWordGroupRequest struct {
	GroupID     string   `json:"group_id"`
	Term        string   `json:"term"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	Lock        bool     `json:"lock"`
}

// CreatedAlias is one persisted alias row under a term.
type CreatedAlias struct {
	ID   string `json:"id"`
	Word string `json:"word"`
}

// CheckWordsExistRequest is the JSON body for POST /word_group:checkExists.
type CheckWordsExistRequest struct {
	Term    string   `json:"term"`
	Aliases []string `json:"aliases"`
}

// CheckWordsExistResponse lists which submitted words already appear in words for the request user (any group/kind).
type CheckWordsExistResponse struct {
	Existing []string `json:"existing"`
}

// DeleteWordGroupResponse is returned in APIResponse.Data after delete.
type DeleteWordGroupResponse struct {
	GroupID     string `json:"group_id"`
	DeletedRows int64  `json:"deleted_rows"`
}

// BatchDeleteWordGroupsRequest is the JSON body for POST /word_group:batchDelete.
type BatchDeleteWordGroupsRequest struct {
	GroupIDs []string `json:"group_ids"`
}

// BatchDeleteWordGroupsResponse is returned in APIResponse.Data after batch soft-delete.
type BatchDeleteWordGroupsResponse struct {
	GroupIDs    []string `json:"group_ids"`
	DeletedRows int64    `json:"deleted_rows"`
}

// MergeWordGroupsRequest is the JSON body for POST /word_group:merge.
type MergeWordGroupsRequest struct {
	GroupIDs    []string `json:"group_ids"`
	Term        string   `json:"term"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

// MergeAndAddWordRequest applies one or more merge specs (same shape as MergeWordGroupsRequest),
// then optionally adds word as alias into existing groups listed in group_ids.
type MergeAndAddWordRequest struct {
	ID       string                   `json:"id"`
	Word     string                   `json:"word"`
	GroupIDs []string                 `json:"group_ids"`
	Merges   []MergeWordGroupsRequest `json:"merges"`
}

// MergeAndAddWordResponse is returned after all merge batches complete.
type MergeAndAddWordResponse struct {
	Items []CreateWordGroupResponse `json:"items"`
}

// CreateWordGroupResponse is returned in APIResponse.Data after create.
type CreateWordGroupResponse struct {
	TermID      string         `json:"term_id"`
	Term        string         `json:"term"`
	GroupID     string         `json:"group_id"`
	Aliases     []CreatedAlias `json:"aliases"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Reference   string         `json:"reference"`
	Lock        bool           `json:"lock"`
}

// ListWordGroupsResponse is returned in APIResponse.Data for GET /word_group.
type ListWordGroupsResponse struct {
	Items         []CreateWordGroupResponse `json:"items"`
	TotalSize     int32                     `json:"total_size"`
	NextPageToken string                    `json:"next_page_token"`
}

// SearchWordGroupsRequest is the JSON body for POST /word_group:search.
type SearchWordGroupsRequest struct {
	Keyword   string `json:"keyword"`
	Source    string `json:"source"`
	PageToken string `json:"page_token"`
	PageSize  int    `json:"page_size"`
}

// CreateWordGroup persists one term row and zero or more alias rows (same group / metadata).
func CreateWordGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body CreateWordGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	term := strings.TrimSpace(body.Term)
	groupID := common.GenerateID()
	desc := strings.TrimSpace(body.Description)
	ref := ""
	src := normalizeSource("")
	aliases := normalizeAliases(body.Aliases)
	conflictID := strings.TrimSpace(body.ID)

	if term == "" {
		common.ReplyErr(w, "term is required", http.StatusBadRequest)
		return
	}
	if msg := validateTermAndAliases(term, aliases); msg != "" {
		common.ReplyErr(w, msg, http.StatusBadRequest)
		return
	}
	if body.Conflict && conflictID == "" {
		common.ReplyErr(w, "id is required when conflict is true", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	base := orm.WordBase{
		CreateUserID:   userID,
		CreateUserName: userName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	var termID string
	var createdAliases []CreatedAlias

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
			Locked:        body.Lock,
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
				Locked:        body.Lock,
				WordBase:      base,
			}
			if err := tx.Create(&ar).Error; err != nil {
				return err
			}
			createdAliases = append(createdAliases, CreatedAlias{ID: aid, Word: a})
		}
		if body.Conflict {
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
		}
		return nil
	})
	if errors.Is(err, errWordGroupConflictNotFound) {
		common.ReplyErr(w, "word group conflict not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Logger.Error().Err(err).Str("term_id", termID).Msg("create word_group rows failed")
		common.ReplyErr(w, "create word group failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, CreateWordGroupResponse{
		TermID:      termID,
		Term:        term,
		GroupID:     groupID,
		Aliases:     createdAliases,
		Description: desc,
		Source:      src,
		Reference:   ref,
		Lock:        body.Lock,
	})
}

// UpdateWordGroup updates an existing word group owned by the request user (term + replaces all alias rows).
func UpdateWordGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body UpdateWordGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	groupID := strings.TrimSpace(body.GroupID)
	termText := strings.TrimSpace(body.Term)
	desc := strings.TrimSpace(body.Description)
	aliases := normalizeAliases(body.Aliases)

	if groupID == "" {
		common.ReplyErr(w, "group_id is required", http.StatusBadRequest)
		return
	}
	if termText == "" {
		common.ReplyErr(w, "term is required", http.StatusBadRequest)
		return
	}
	if msg := validateTermAndAliases(termText, aliases); msg != "" {
		common.ReplyErr(w, msg, http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var out CreateWordGroupResponse
	err := store.DB().Transaction(func(tx *gorm.DB) error {
		var termRow orm.Word
		if err := tx.Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL",
			groupID, userID, orm.WordKindTerm).
			First(&termRow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errWordGroupNotFound
			}
			return err
		}

		now := time.Now().UTC()
		if err := tx.Model(&orm.Word{}).
			Where("id = ? AND create_user_id = ?", termRow.ID, userID).
			Updates(map[string]interface{}{
				"word":        termText,
				"description": desc,
				"locked":      body.Lock,
				"updated_at":  now,
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&orm.Word{}).
			Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL",
				groupID, userID, orm.WordKindAlias).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}

		createdAliases := make([]CreatedAlias, 0, len(aliases))
		base := orm.WordBase{
			CreateUserID:   termRow.CreateUserID,
			CreateUserName: termRow.CreateUserName,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		for _, a := range aliases {
			aid := common.GenerateID()
			ar := orm.Word{
				ID:            aid,
				Word:          a,
				WordKind:      orm.WordKindAlias,
				GroupID:       groupID,
				Description:   desc,
				Source:        termRow.Source,
				ReferenceInfo: termRow.ReferenceInfo,
				Locked:        body.Lock,
				WordBase:      base,
			}
			if err := tx.Create(&ar).Error; err != nil {
				return err
			}
			createdAliases = append(createdAliases, CreatedAlias{ID: aid, Word: a})
		}

		out = CreateWordGroupResponse{
			TermID:      termRow.ID,
			Term:        termText,
			GroupID:     groupID,
			Aliases:     createdAliases,
			Description: desc,
			Source:      termRow.Source,
			Reference:   termRow.ReferenceInfo,
			Lock:        body.Lock,
		}
		return nil
	})
	if errors.Is(err, errWordGroupNotFound) {
		common.ReplyErr(w, "word group not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Logger.Error().Err(err).Str("group_id", groupID).Msg("update word_group failed")
		common.ReplyErr(w, "update word group failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, out)
}

// errWordGroupNotFound is returned from UpdateWordGroup transaction when the term row is missing.
var errWordGroupNotFound = errors.New("word group not found")

// errWordGroupConflictNotFound is returned when expected conflict row is missing on conflict-create flow.
var errWordGroupConflictNotFound = errors.New("word group conflict not found")

// errInvalidWordGroupSource indicates an unsupported source filter value.
var errInvalidWordGroupSource = errors.New("invalid source")

// escapeLikePatternForBangEscape quotes LIKE wildcards for "... LIKE ? ESCAPE '!'".
// Backslashes stay literal, so substring search for `\` works on MySQL (where `\` is otherwise LIKE's default escape).
func escapeLikePatternForBangEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '!':
			b.WriteString("!!")
		case '%':
			b.WriteString("!%")
		case '_':
			b.WriteString("!_")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wordGroupMatchQuery scopes to active word rows for userID (term and alias rows; no word_kind filter).
// Keyword matches word as substring (LIKE); uses original input without lowercasing; source filters by the row's source column.
func wordGroupMatchQuery(db *gorm.DB, userID, keyword, sourceRaw string) (*gorm.DB, error) {
	q := db.Model(&orm.Word{}).
		Where("create_user_id = ? AND deleted_at IS NULL", userID)
	if strings.TrimSpace(sourceRaw) != "" {
		src := normalizeSource(sourceRaw)
		if src == "" {
			return nil, errInvalidWordGroupSource
		}
		q = q.Where("source = ?", src)
	}
	if kw := strings.TrimSpace(keyword); kw != "" {
		like := "%" + escapeLikePatternForBangEscape(kw) + "%"
		q = q.Where("word LIKE ? ESCAPE '!'", like)
	}
	return q, nil
}

// countMatchedWordGroups returns DISTINCT group_id count for the same match as list/search.
func countMatchedWordGroups(mq *gorm.DB) (int64, error) {
	var total int64
	// COUNT(DISTINCT group_id)
	if err := mq.Session(&gorm.Session{}).Distinct("group_id").Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// findTermRowsForMatchedGroups returns term rows for groups matching mq, ordered by term updated_at DESC.
func findTermRowsForMatchedGroups(db *gorm.DB, mq *gorm.DB, userID string, offset, pageSize int) ([]orm.Word, error) {
	gidSub := mq.Session(&gorm.Session{}).Distinct("group_id").Select("group_id")
	var terms []orm.Word
	err := db.Model(&orm.Word{}).
		Where("create_user_id = ? AND deleted_at IS NULL AND word_kind = ?", userID, orm.WordKindTerm).
		Where("group_id IN (?)", gidSub).
		Order("updated_at DESC, id ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&terms).Error
	return terms, err
}

// loadCreateWordGroupResponses loads alias rows for the given term rows and builds list payload.
func loadCreateWordGroupResponses(db *gorm.DB, userID string, terms []orm.Word) ([]CreateWordGroupResponse, error) {
	if len(terms) == 0 {
		return []CreateWordGroupResponse{}, nil
	}
	groupIDs := make([]string, 0, len(terms))
	for i := range terms {
		groupIDs = append(groupIDs, terms[i].GroupID)
	}
	var aliasRows []orm.Word
	if err := db.Where("group_id IN ? AND create_user_id = ? AND deleted_at IS NULL AND word_kind = ?",
		groupIDs, userID, orm.WordKindAlias).
		Order("group_id ASC, id ASC").
		Find(&aliasRows).Error; err != nil {
		return nil, err
	}
	aliasByGroup := make(map[string][]CreatedAlias, len(groupIDs))
	for i := range aliasRows {
		a := &aliasRows[i]
		aliasByGroup[a.GroupID] = append(aliasByGroup[a.GroupID], CreatedAlias{ID: a.ID, Word: a.Word})
	}
	items := make([]CreateWordGroupResponse, 0, len(terms))
	for i := range terms {
		t := &terms[i]
		aliases := aliasByGroup[t.GroupID]
		if aliases == nil {
			aliases = []CreatedAlias{}
		}
		items = append(items, CreateWordGroupResponse{
			TermID:      t.ID,
			Term:        t.Word,
			GroupID:     t.GroupID,
			Aliases:     aliases,
			Description: t.Description,
			Source:      t.Source,
			Reference:   t.ReferenceInfo,
			Lock:        t.Locked,
		})
	}
	return items, nil
}

func replyWordGroupListPage(w http.ResponseWriter, db *gorm.DB, userID string, terms []orm.Word, total int64, offset, pageSize int, errMsg string) {
	items, err := loadCreateWordGroupResponses(db, userID, terms)
	if err != nil {
		log.Logger.Error().Err(err).Msg("word_group list items failed")
		common.ReplyErr(w, errMsg, http.StatusInternalServerError)
		return
	}
	end := offset + len(items)
	nextToken := ""
	if end < int(total) {
		nextToken = encodeListPageToken(end, pageSize, int(total))
	}
	common.ReplyOK(w, ListWordGroupsResponse{
		Items:         items,
		TotalSize:     int32(total),
		NextPageToken: nextToken,
	})
}

// CheckWordsExist returns words among term + aliases that already exist in the words table for the request user.
func CheckWordsExist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body CheckWordsExistRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	candidates := uniqueWordCandidates(body.Term, body.Aliases)
	if len(candidates) == 0 {
		common.ReplyErr(w, "term and aliases are empty", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var hits []string
	q := store.DB().Model(&orm.Word{}).
		Where("word IN ? AND create_user_id = ? AND deleted_at IS NULL", candidates, userID).
		Distinct("word").
		Pluck("word", &hits)
	if q.Error != nil {
		log.Logger.Error().Err(q.Error).Msg("check words exist query failed")
		common.ReplyErr(w, "check words exist failed", http.StatusInternalServerError)
		return
	}
	hit := make(map[string]struct{}, len(hits))
	for _, s := range hits {
		hit[s] = struct{}{}
	}
	existing := make([]string, 0, len(hits))
	for _, c := range candidates {
		if _, ok := hit[c]; ok {
			existing = append(existing, c)
		}
	}
	common.ReplyOK(w, CheckWordsExistResponse{Existing: existing})
}

// ListWordGroups returns active word groups for the request user, ordered by each group's term row updated_at DESC.
// Query: page_token (offset), page_size (default 20, max 100); same semantics as dataset list.
func ListWordGroups(w http.ResponseWriter, r *http.Request) {
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
	matchQ, err := wordGroupMatchQuery(db, userID, "", "")
	if err != nil {
		log.Logger.Error().Err(err).Msg("list word_group scope failed")
		common.ReplyErr(w, "list word group failed", http.StatusInternalServerError)
		return
	}

	total, err := countMatchedWordGroups(matchQ)
	if err != nil {
		log.Logger.Error().Err(err).Msg("list word_group count failed")
		common.ReplyErr(w, "list word group failed", http.StatusInternalServerError)
		return
	}

	terms, err := findTermRowsForMatchedGroups(db, matchQ, userID, offset, pageSize)
	if err != nil {
		log.Logger.Error().Err(err).Msg("list word_group query failed")
		common.ReplyErr(w, "list word group failed", http.StatusInternalServerError)
		return
	}

	replyWordGroupListPage(w, db, userID, terms, total, offset, pageSize, "list word group failed")
}

// SearchWordGroups searches word rows (term or alias) by keyword substring; total is distinct group_id; results ordered by term updated_at DESC.
func SearchWordGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body SearchWordGroupsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}

	pageSize := body.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if tok := strings.TrimSpace(body.PageToken); tok != "" {
		if v, err := parseListPageToken(tok); err == nil && v >= 0 {
			offset = v
		}
	}

	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	db := store.DB()
	matchQ, err := wordGroupMatchQuery(db, userID, body.Keyword, body.Source)
	if err != nil {
		if errors.Is(err, errInvalidWordGroupSource) {
			common.ReplyErr(w, "invalid source", http.StatusBadRequest)
			return
		}
		log.Logger.Error().Err(err).Msg("search word_group scope failed")
		common.ReplyErr(w, "search word group failed", http.StatusInternalServerError)
		return
	}

	total, err := countMatchedWordGroups(matchQ)
	if err != nil {
		log.Logger.Error().Err(err).Msg("search word_group count failed")
		common.ReplyErr(w, "search word group failed", http.StatusInternalServerError)
		return
	}

	terms, err := findTermRowsForMatchedGroups(db, matchQ, userID, offset, pageSize)
	if err != nil {
		log.Logger.Error().Err(err).Msg("search word_group query failed")
		common.ReplyErr(w, "search word group failed", http.StatusInternalServerError)
		return
	}

	replyWordGroupListPage(w, db, userID, terms, total, offset, pageSize, "search word group failed")
}

// MergeWordGroups merges group_ids[1:] into group_ids[0]: soft-deletes all active word rows under those
// groups, then recreates the master group from term, aliases, and description (metadata from former master term).
func MergeWordGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body MergeWordGroupsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}

	term, desc, aliases, groupIDs, errMsg := normalizeMergeWordGroupsRequest(body)
	if errMsg != "" {
		common.ReplyErr(w, errMsg, http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	userName := store.UserName(r)

	masterGID := groupIDs[0]

	db := store.DB()
	err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		_, err := mergeWordGroupsInTx(tx, userID, userName, groupIDs, term, desc, aliases, now)
		return err
	})
	if errors.Is(err, errWordGroupNotFound) {
		common.ReplyErr(w, "word group not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Logger.Error().Err(err).Msg("merge word_group failed")
		common.ReplyErr(w, "merge word group failed", http.StatusInternalServerError)
		return
	}

	out, ok, err := buildCreateWordGroupResponse(db, userID, masterGID)
	if err != nil {
		log.Logger.Error().Err(err).Str("group_id", masterGID).Msg("merge word_group reload failed")
		common.ReplyErr(w, "merge word group failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		common.ReplyErr(w, "merge word group failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, out)
}

// MergeWordGroupsAndAddWord runs each merge spec, adds word into group_ids, then resolves the conflict.
func MergeWordGroupsAndAddWord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body MergeAndAddWordRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
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
	if len(body.Merges) == 0 {
		common.ReplyErr(w, "merges is required", http.StatusBadRequest)
		return
	}
	for i, merge := range body.Merges {
		if msg := validateMergeWordGroupsRequest(merge); msg != "" {
			common.ReplyErr(w, fmt.Sprintf("merges[%d]: %s", i, msg), http.StatusBadRequest)
			return
		}
	}

	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	userName := store.UserName(r)
	addToGroupIDs := dedupeGroupIDsPreserveOrder(body.GroupIDs)

	masterGIDs := make([]string, 0, len(body.Merges))
	db := store.DB()
	err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, merge := range body.Merges {
			term, desc, aliases, groupIDs, _ := normalizeMergeWordGroupsRequest(merge)
			masterGID, err := mergeWordGroupsInTx(tx, userID, userName, groupIDs, term, desc, aliases, now)
			if err != nil {
				return err
			}
			masterGIDs = append(masterGIDs, masterGID)
		}
		if len(addToGroupIDs) > 0 {
			if _, _, err := addConflictWordToGroupsInTx(tx, userID, word, addToGroupIDs, now); err != nil {
				return err
			}
		}

		res := tx.Model(&orm.WordGroupConflict{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conflictID, userID).
			Updates(map[string]interface{}{
				"deleted_at": now,
				"updated_at": now,
			})
		if err := res.Error; err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return errWordGroupConflictNotFound
		}
		return nil
	})
	if errors.Is(err, errWordGroupConflictNotFound) {
		common.ReplyErr(w, "word group conflict not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, errWordGroupNotFound) {
		common.ReplyErr(w, "word group not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Logger.Error().Err(err).Str("word", word).Msg("merge and add word_group failed")
		common.ReplyErr(w, "merge and add word group failed", http.StatusInternalServerError)
		return
	}

	items := make([]CreateWordGroupResponse, 0, len(masterGIDs))
	for _, masterGID := range masterGIDs {
		out, ok, err := buildCreateWordGroupResponse(db, userID, masterGID)
		if err != nil {
			log.Logger.Error().Err(err).Str("group_id", masterGID).Msg("merge and add word_group reload failed")
			common.ReplyErr(w, "merge and add word group failed", http.StatusInternalServerError)
			return
		}
		if !ok {
			common.ReplyErr(w, "merge and add word group failed", http.StatusInternalServerError)
			return
		}
		items = append(items, out)
	}
	common.ReplyOK(w, MergeAndAddWordResponse{Items: items})
}

func validateMergeWordGroupsRequest(req MergeWordGroupsRequest) string {
	groupIDs := dedupeGroupIDsPreserveOrder(req.GroupIDs)
	if len(groupIDs) < 2 {
		return "at least 2 group_ids required"
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return "term is required"
	}
	return validateTermAndAliases(term, normalizeAliases(req.Aliases))
}

func normalizeMergeWordGroupsRequest(req MergeWordGroupsRequest) (term, desc string, aliases, groupIDs []string, errMsg string) {
	groupIDs = dedupeGroupIDsPreserveOrder(req.GroupIDs)
	if len(groupIDs) < 2 {
		return "", "", nil, nil, "at least 2 group_ids required"
	}
	term = strings.TrimSpace(req.Term)
	desc = strings.TrimSpace(req.Description)
	aliases = normalizeAliases(req.Aliases)
	if term == "" {
		return "", "", nil, nil, "term is required"
	}
	if msg := validateTermAndAliases(term, aliases); msg != "" {
		return "", "", nil, nil, msg
	}
	return term, desc, aliases, groupIDs, ""
}

// mergeWordGroupsInTx soft-deletes merged groups' words and recreates the master group from term/aliases/description.
func mergeWordGroupsInTx(tx *gorm.DB, userID, userName string, groupIDs []string, term, desc string, aliases []string, now time.Time) (masterGID string, err error) {
	masterGID = groupIDs[0]
	slaveGIDs := groupIDs[1:]

	var masterTerm orm.Word
	if err := tx.Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL",
		masterGID, userID, orm.WordKindTerm).
		First(&masterTerm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", errWordGroupNotFound
		}
		return "", err
	}

	for _, gid := range slaveGIDs {
		var n int64
		if err := tx.Model(&orm.Word{}).
			Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL", gid, userID, orm.WordKindTerm).
			Count(&n).Error; err != nil {
			return "", err
		}
		if n == 0 {
			return "", errWordGroupNotFound
		}
	}

	allGIDs := append([]string{masterGID}, slaveGIDs...)
	if err := tx.Model(&orm.Word{}).
		Where("group_id IN ? AND create_user_id = ? AND deleted_at IS NULL", allGIDs, userID).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"updated_at": now,
		}).Error; err != nil {
		return "", err
	}

	base := orm.WordBase{
		CreateUserID:   userID,
		CreateUserName: userName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	termRow := orm.Word{
		ID:            common.GenerateID(),
		Word:          term,
		WordKind:      orm.WordKindTerm,
		GroupID:       masterGID,
		Description:   desc,
		Source:        masterTerm.Source,
		ReferenceInfo: masterTerm.ReferenceInfo,
		Locked:        masterTerm.Locked,
		WordBase:      base,
	}
	if err := tx.Create(&termRow).Error; err != nil {
		return "", err
	}
	for _, a := range aliases {
		ar := orm.Word{
			ID:            common.GenerateID(),
			Word:          a,
			WordKind:      orm.WordKindAlias,
			GroupID:       masterGID,
			Description:   desc,
			Source:        masterTerm.Source,
			ReferenceInfo: masterTerm.ReferenceInfo,
			Locked:        masterTerm.Locked,
			WordBase:      base,
		}
		if err := tx.Create(&ar).Error; err != nil {
			return "", err
		}
	}

	if err := remapWordGroupConflictsSlaveGroupIDs(tx, userID, masterGID, slaveGIDs, now); err != nil {
		return "", err
	}
	return masterGID, nil
}

// buildCreateWordGroupResponse loads one active word group by group_id for the user.
// ok is false when the group or term row is missing (not an error). err is set on DB failure.
func buildCreateWordGroupResponse(db *gorm.DB, userID, groupID string) (resp CreateWordGroupResponse, ok bool, err error) {
	var rows []orm.Word
	if err := db.Where("group_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, userID).
		Order("word_kind DESC, id ASC").
		Find(&rows).Error; err != nil {
		return CreateWordGroupResponse{}, false, err
	}
	if len(rows) == 0 {
		return CreateWordGroupResponse{}, false, nil
	}

	var termRow *orm.Word
	aliases := make([]CreatedAlias, 0)
	for i := range rows {
		row := &rows[i]
		if row.WordKind == orm.WordKindTerm {
			if termRow == nil {
				termRow = row
			}
			continue
		}
		if row.WordKind == orm.WordKindAlias {
			aliases = append(aliases, CreatedAlias{ID: row.ID, Word: row.Word})
		}
	}
	if termRow == nil {
		return CreateWordGroupResponse{}, false, nil
	}

	return CreateWordGroupResponse{
		TermID:      termRow.ID,
		Term:        termRow.Word,
		GroupID:     groupID,
		Aliases:     aliases,
		Description: termRow.Description,
		Source:      termRow.Source,
		Reference:   termRow.ReferenceInfo,
		Lock:        termRow.Locked,
	}, true, nil
}

// GetWordGroup returns one active word group for path group_id (same payload shape as create response).
func GetWordGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	groupID := strings.TrimSpace(common.PathVar(r, "group_id"))
	if groupID == "" {
		common.ReplyErr(w, "missing group_id", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	out, ok, err := buildCreateWordGroupResponse(store.DB(), userID, groupID)
	if err != nil {
		log.Logger.Error().Err(err).Str("group_id", groupID).Msg("get word_group query failed")
		common.ReplyErr(w, "get word group failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		common.ReplyErr(w, "word group not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, out)
}

// deleteWordGroupsForUser soft-deletes active word rows for the given group_ids owned by userID.
// It returns distinct group_ids that had at least one active row, and total rows updated.
func deleteWordGroupsForUser(db *gorm.DB, userID string, groupIDs []string) ([]string, int64, error) {
	if len(groupIDs) == 0 {
		return nil, 0, nil
	}
	var hitGroups []string
	if err := db.Model(&orm.Word{}).
		Where("group_id IN ? AND create_user_id = ? AND deleted_at IS NULL", groupIDs, userID).
		Distinct("group_id").
		Pluck("group_id", &hitGroups).Error; err != nil {
		return nil, 0, err
	}
	if len(hitGroups) == 0 {
		return nil, 0, nil
	}
	now := time.Now().UTC()
	tx := db.Model(&orm.Word{}).
		Where("group_id IN ? AND create_user_id = ? AND deleted_at IS NULL", hitGroups, userID).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"updated_at": now,
		})
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	return hitGroups, tx.RowsAffected, nil
}

// DeleteWordGroup soft-deletes all active words rows for the given group_id owned by the request user.
func DeleteWordGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	groupID := strings.TrimSpace(common.PathVar(r, "group_id"))
	if groupID == "" {
		common.ReplyErr(w, "missing group_id", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	db := store.DB()
	hitGroups, rows, err := deleteWordGroupsForUser(db, userID, []string{groupID})
	if err != nil {
		log.Logger.Error().Err(err).Str("group_id", groupID).Msg("delete word_group failed")
		common.ReplyErr(w, "delete word group failed", http.StatusInternalServerError)
		return
	}
	if len(hitGroups) == 0 {
		common.ReplyErr(w, "word group not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, DeleteWordGroupResponse{GroupID: groupID, DeletedRows: rows})
}

// BatchDeleteWordGroups soft-deletes active word rows for each requested group_id owned by the request user.
func BatchDeleteWordGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body BatchDeleteWordGroupsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	uniqueIDs := make([]string, 0, len(body.GroupIDs))
	seen := make(map[string]struct{}, len(body.GroupIDs))
	for _, id := range body.GroupIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		common.ReplyErr(w, "group_ids required", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	db := store.DB()
	hitGroups, rows, err := deleteWordGroupsForUser(db, userID, uniqueIDs)
	if err != nil {
		log.Logger.Error().Err(err).Msg("batch delete word_group failed")
		common.ReplyErr(w, "batch delete word group failed", http.StatusInternalServerError)
		return
	}
	if len(hitGroups) == 0 {
		common.ReplyErr(w, "word group not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, BatchDeleteWordGroupsResponse{GroupIDs: hitGroups, DeletedRows: rows})
}

func uniqueWordCandidates(term string, aliases []string) []string {
	term = strings.TrimSpace(term)
	var out []string
	seen := make(map[string]struct{})
	if term != "" {
		out = append(out, term)
		seen[term] = struct{}{}
	}
	for _, a := range normalizeAliases(aliases) {
		if _, ok := seen[a]; ok {
			continue
		}
		out = append(out, a)
		seen[a] = struct{}{}
	}
	return out
}

// remapWordGroupConflictsSlaveGroupIDs replaces every slave group id in conflict rows' group_ids JSON
// with masterGID for the given user (active rows only). If after replacement all group ids are the same,
// the conflict word is inserted as an alias into that group and the conflict row is soft-deleted.
func remapWordGroupConflictsSlaveGroupIDs(tx *gorm.DB, userID, masterGID string, slaveGIDs []string, now time.Time) error {
	slaveSet := make(map[string]struct{}, len(slaveGIDs))
	for _, id := range slaveGIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		slaveSet[id] = struct{}{}
	}
	if len(slaveSet) == 0 {
		return nil
	}
	var conflictRows []orm.WordGroupConflict
	if err := tx.Where("create_user_id = ? AND deleted_at IS NULL", userID).Find(&conflictRows).Error; err != nil {
		return err
	}
	for i := range conflictRows {
		cr := &conflictRows[i]
		gids, err := parseJSONStringSliceField(cr.GroupIDs)
		if err != nil {
			return err
		}
		if len(gids) == 0 {
			continue
		}
		changed := false
		newGids := make([]string, len(gids))
		copy(newGids, gids)
		for j := range newGids {
			if _, ok := slaveSet[strings.TrimSpace(newGids[j])]; ok {
				newGids[j] = masterGID
				changed = true
			}
		}
		if !changed {
			continue
		}
		newGids = dedupeGroupIDsPreserveOrder(newGids)
		if len(newGids) == 0 {
			continue
		}
		if groupIDSliceAllEqual(newGids) {
			if err := insertConflictWordAsAliasAndResolveConflict(tx, userID, cr, newGids[0], now); err != nil {
				return err
			}
			continue
		}
		outJSON, err := json.Marshal(newGids)
		if err != nil {
			return err
		}
		if err := tx.Model(&orm.WordGroupConflict{}).Where("id = ?", cr.ID).Updates(map[string]interface{}{
			"group_ids":  string(outJSON),
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func groupIDSliceAllEqual(gids []string) bool {
	if len(gids) == 0 {
		return false
	}
	g0 := gids[0]
	for _, g := range gids[1:] {
		if g != g0 {
			return false
		}
	}
	return true
}

func insertConflictWordAsAliasAndResolveConflict(tx *gorm.DB, userID string, cr *orm.WordGroupConflict, targetGID string, now time.Time) error {
	var termRow orm.Word
	if err := tx.Where("group_id = ? AND create_user_id = ? AND word_kind = ? AND deleted_at IS NULL",
		targetGID, userID, orm.WordKindTerm).First(&termRow).Error; err != nil {
		return err
	}
	cWord := strings.TrimSpace(cr.Word)
	if cWord != "" {
		var n int64
		if err := tx.Model(&orm.Word{}).
			Where("group_id = ? AND create_user_id = ? AND word = ? AND deleted_at IS NULL", targetGID, userID, cWord).
			Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			row := orm.Word{
				ID:            common.GenerateID(),
				Word:          cWord,
				WordKind:      orm.WordKindAlias,
				GroupID:       targetGID,
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
				return err
			}
		}
	}
	return tx.Model(&orm.WordGroupConflict{}).Where("id = ?", cr.ID).Updates(map[string]interface{}{
		"deleted_at": now,
		"updated_at": now,
	}).Error
}

func dedupeGroupIDsPreserveOrder(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func aliasesContainWord(aliases []string, word string) bool {
	for _, a := range aliases {
		if a == word {
			return true
		}
	}
	return false
}

// validateTermAndAliases returns a non-empty API error message if term equals any alias
// or if aliases contain duplicates (after trimming, same as normalizeAliases).
func validateTermAndAliases(term string, aliases []string) string {
	seen := make(map[string]struct{}, len(aliases))
	for _, a := range aliases {
		if a == term {
			return "term must not match any alias"
		}
		if _, ok := seen[a]; ok {
			return "aliases must be unique"
		}
		seen[a] = struct{}{}
	}
	return ""
}

func normalizeAliases(raw []string) []string {
	var out []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func normalizeSource(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "", "user", "用户":
		return "user"
	case "ai", "系统":
		return "ai"
	default:
		return ""
	}
}
