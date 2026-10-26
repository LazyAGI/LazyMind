package academic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

// SourceContext refers to server-owned evidence, never arbitrary client URLs.
type SourceContext struct {
	WorkflowSessionID string `json:"workflow_session_id,omitempty"`
	WorkDisplayKey    string `json:"work_display_key,omitempty"`
	ConversationID    string `json:"conversation_id,omitempty"`
	HistoryID         string `json:"history_id,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
}

func sourceHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (c SourceContext) key() string { return sourceHash(c) }

type sourceRecord struct {
	ID         string `json:"source_id"`
	CitationID string `json:"citation_id"`
	Index      any    `json:"index"`
	URL        string `json:"url"`
	Title      string `json:"title"`
}
type SourceInspection struct {
	SourceID  string            `json:"source_id"`
	URL       string            `json:"url"`
	Title     string            `json:"title"`
	Eligible  bool              `json:"eligible"`
	PaperKey  string            `json:"paper_key,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Work      WorkInput         `json:"-"`
	Candidate FulltextCandidate `json:"-"`
}

var strictArxivPath = regexp.MustCompile(`^/(?:abs|pdf|html)/((?:[0-9]{2}(?:0[1-9]|1[0-2])\.[0-9]{4,5}|[a-z-]+(?:\.[A-Z]{2})?/[0-9]{2}(?:0[1-9]|1[0-2])[0-9]{3}))(?:v[1-9][0-9]*)?(?:\.pdf)?/?$`)

func officialArxivID(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "arxiv.org" && host != "www.arxiv.org" && host != "export.arxiv.org" {
		return ""
	}
	m := strictArxivPath.FindStringSubmatch(u.Path)
	if len(m) != 2 {
		return ""
	}
	return strings.ToLower(m[1])
}
func sourceRecordID(s sourceRecord) string {
	if s.ID != "" {
		return s.ID
	}
	if s.CitationID != "" {
		return s.CitationID
	}
	if s.Index != nil {
		return fmt.Sprint(s.Index)
	}
	return s.URL
}
func decodeSources(raw json.RawMessage) ([]sourceRecord, error) {
	var envelope struct {
		Sources json.RawMessage `json:"sources"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Sources) > 0 {
		raw = envelope.Sources
	}
	var rows []sourceRecord
	if len(raw) == 0 || string(raw) == "null" {
		return []sourceRecord{}, nil
	}
	if json.Unmarshal(raw, &rows) == nil {
		return rows, nil
	}
	var indexed map[string]sourceRecord
	if e := json.Unmarshal(raw, &indexed); e != nil {
		return nil, e
	}
	keys := make([]string, 0, len(indexed))
	for k := range indexed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := indexed[k]
		if s.CitationID == "" && s.ID == "" && s.Index == nil {
			s.CitationID = k
		}
		rows = append(rows, s)
	}
	return rows, nil
}
func loadSourceRecords(ctx context.Context, db *gorm.DB, user string, c SourceContext) ([]sourceRecord, error) {
	if user == "" {
		return nil, errors.New("source access denied")
	}
	var raw json.RawMessage
	if c.WorkflowSessionID != "" {
		if c.TaskID != "" || c.HistoryID != "" || c.WorkDisplayKey == "" {
			return nil, errors.New("invalid Work context")
		}
		sources, e := workflow.ReadOrdinaryTaskSources(ctx, db, user, c.WorkflowSessionID, c.WorkDisplayKey)
		if e != nil {
			return nil, e
		}
		raw, _ = json.Marshal(sources)
	} else if c.TaskID != "" {
		if c.HistoryID != "" {
			return nil, errors.New("choose chat history or work task")
		}
		var task orm.SubAgentTask
		q := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", c.TaskID, user)
		if c.ConversationID != "" {
			q = q.Where("conversation_id = ?", c.ConversationID)
		}
		if e := q.Take(&task).Error; e != nil {
			return nil, errors.New("source task unavailable")
		}
		raw = json.RawMessage(task.Sources)
	} else {
		if c.ConversationID == "" || c.HistoryID == "" {
			return nil, errors.New("conversation_id and history_id required")
		}
		var conv orm.Conversation
		if e := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", c.ConversationID, user).Take(&conv).Error; e != nil {
			return nil, errors.New("source conversation unavailable")
		}
		var h orm.ChatHistory
		e := db.WithContext(ctx).Where("id = ? AND conversation_id = ?", c.HistoryID, c.ConversationID).Take(&h).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			var mh orm.MultiAnswersChatHistory
			if e = db.WithContext(ctx).Where("id = ? AND conversation_id = ?", c.HistoryID, c.ConversationID).Take(&mh).Error; e != nil {
				return nil, errors.New("source answer unavailable")
			}
			raw = mh.RetrievalResult
		} else if e != nil {
			return nil, e
		} else {
			raw = h.RetrievalResult
		}
	}
	return decodeSources(raw)
}

// Recognition is deliberately offline. Only persisted academic identities may
// expand recognition beyond official arXiv URLs; a DOI-shaped string is insufficient.
func inspectRecord(ctx context.Context, db *gorm.DB, s sourceRecord) (SourceInspection, error) {
	out := SourceInspection{SourceID: sourceRecordID(s), URL: s.URL, Title: s.Title, Reason: "无法确认文献身份或缺少原文地址"}
	id := officialArxivID(s.URL)
	if id != "" {
		out.Work = WorkInput{ArxivID: id, Title: s.Title, Provider: "arxiv", ProviderWorkID: id}
		out.PaperKey = "arxiv:" + id
		out.Candidate = FulltextCandidate{URL: "https://arxiv.org/pdf/" + id, SourceType: "official_pdf", SourceProvider: "arxiv", ExpectedMIME: "application/pdf"}
		// Prefer an already established cross-provider identity.
		var known orm.AcademicWork
		if e := db.WithContext(ctx).Where("arxiv_id_base = ?", id).Take(&known).Error; e == nil {
			out.Work.WorkID = known.ID
			if known.DOINormalized != "" {
				out.PaperKey = "doi:" + known.DOINormalized
			}
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return out, e
		}
	} else {
		u, e := url.Parse(s.URL)
		if e != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return out, nil
		}
		var known orm.AcademicWork
		var linked orm.AcademicWorkDocument
		// A persisted acquisition is trusted evidence of the original locator.
		if e = db.WithContext(ctx).Where("source_locator = ?", s.URL).Take(&linked).Error; e == nil {
			e = db.WithContext(ctx).Where("id = ?", linked.AcademicWorkID).Take(&known).Error
		} else if strings.EqualFold(u.Hostname(), "doi.org") || strings.EqualFold(u.Hostname(), "dx.doi.org") {
			doi := strings.ToLower(strings.TrimPrefix(u.Path, "/"))
			if !doiPattern.MatchString(doi) {
				return out, nil
			}
			e = db.WithContext(ctx).Where("doi_normalized = ?", doi).Take(&known).Error
		} else {
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return out, e
			}
			// Existing resolver metadata may identify a direct original URL. Read
			// the trusted records locally and require an exact URL, never a title match.
			var works []orm.AcademicWork
			if e = db.WithContext(ctx).Where("CAST(metadata_provenance_json AS TEXT) LIKE ?", "%trusted_fulltext%").Find(&works).Error; e != nil {
				return out, e
			}
			for _, work := range works {
				var provenance map[string]any
				_ = json.Unmarshal(work.ProvenanceJSON, &provenance)
				if provenance["trusted_fulltext"] == true && provenance["pdf_url"] == s.URL {
					known = work
					break
				}
			}
			if known.ID == "" {
				return out, nil
			}
		}
		if e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return out, nil
			}
			return out, e
		}
		candidates := candidatesForWork(known)
		if len(candidates) == 0 || (known.DOINormalized == "" && known.ArxivIDBase == "") {
			return out, nil
		}
		// Only resolver-provided PDF provenance or an official arXiv identity qualifies.
		var provenance map[string]any
		_ = json.Unmarshal(known.ProvenanceJSON, &provenance)
		if known.ArxivIDBase == "" && (provenance["pdf_url"] == nil || provenance["trusted_fulltext"] != true) {
			return out, nil
		}
		out.Work = WorkInput{WorkID: known.ID, Title: known.CanonicalTitle, DOI: known.DOINormalized, ArxivID: known.ArxivIDBase}
		out.PaperKey = "doi:" + known.DOINormalized
		if known.DOINormalized == "" {
			out.PaperKey = "arxiv:" + known.ArxivIDBase
		}
		out.Candidate = candidates[0]
	}
	out.Eligible = true
	out.Reason = ""
	return out, nil
}
func inspectSources(ctx context.Context, db *gorm.DB, user string, c SourceContext, ids []string) ([]SourceInspection, error) {
	records, e := loadSourceRecords(ctx, db, user, c)
	if e != nil {
		return nil, e
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := []SourceInspection{}
	for _, s := range records {
		if len(wanted) > 0 && !wanted[sourceRecordID(s)] {
			continue
		}
		v, e := inspectRecord(ctx, db, s)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if len(ids) > 0 {
		found := map[string]bool{}
		for _, s := range out {
			found[s.SourceID] = true
		}
		for _, id := range ids {
			if !found[id] {
				return nil, errors.New("selected source no longer exists")
			}
		}
	}
	return out, nil
}
func InspectSources(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Context SourceContext `json:"source_context"`
		IDs     []string      `json:"source_ids,omitempty"`
	}
	if !parseJSON(r, &req) || len(req.IDs) > 500 {
		replyError(w, 400, "INVALID_REQUEST", "invalid source selection")
		return
	}
	items, e := inspectSources(r.Context(), store.DB(), user, req.Context, req.IDs)
	if e != nil {
		replyError(w, 403, "SOURCE_UNAVAILABLE", e.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": items})
}
