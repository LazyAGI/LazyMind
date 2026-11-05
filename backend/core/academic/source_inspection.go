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
	"slices"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/capability"
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
	ID          string   `json:"source_id"`
	CitationID  string   `json:"citation_id"`
	Index       any      `json:"index"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	DOI         string   `json:"doi"`
	SourceRoles []string `json:"source_roles,omitempty"`
}
type SourceInspection struct {
	Requested *capability.PaperSelection `json:"-"`

	SourceID        string            `json:"source_id"`
	URL             string            `json:"url"`
	Title           string            `json:"title"`
	NeedsPDFLookup  bool              `json:"needs_pdf_lookup,omitempty"`
	PDFLookupFailed bool              `json:"pdf_lookup_failed,omitempty"`
	Eligible        bool              `json:"eligible"`
	PaperKey        string            `json:"paper_key,omitempty"`
	Reason          string            `json:"reason,omitempty"`
	Work            WorkInput         `json:"-"`
	Candidate       FulltextCandidate `json:"-"`
	PDFVerified     bool              `json:"-"`
	Cited           bool              `json:"-"`
}

var strictArxivPath = regexp.MustCompile(`^/(?:abs|pdf|html)/((?:[0-9]{2}(?:0[1-9]|1[0-2])\.[0-9]{4,5}|[a-z-]+(?:\.[A-Z]{2})?/[0-9]{2}(?:0[1-9]|1[0-2])[0-9]{3}))(?:v[1-9][0-9]*)?(?:\.pdf)?/?$`)

func officialArxivID(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "doi.org" || host == "dx.doi.org" {
		id, err := arxivsource.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.ToLower(id.BaseID)
	}
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

// Recognition is offline. A DOI source can be queued for lookup without claiming a PDF exists.
func inspectRecord(ctx context.Context, db *gorm.DB, s sourceRecord) (SourceInspection, error) {
	out := SourceInspection{SourceID: sourceRecordID(s), URL: s.URL, Title: s.Title, Cited: slices.Contains(s.SourceRoles, "cited"), Reason: "无法确认文献身份或缺少原文地址"}
	arxivURL := s.URL
	if arxivURL == "" && NormalizeDOI(s.DOI) != "" {
		arxivURL = "https://doi.org/" + NormalizeDOI(s.DOI)
	}
	id := officialArxivID(arxivURL)
	if id != "" {
		out.Work = WorkInput{ArxivID: id, Title: s.Title, Provider: "arxiv", ProviderWorkID: id}
		out.PaperKey = "arxiv:" + id
		out.Candidate = FulltextCandidate{URL: "https://arxiv.org/pdf/" + id, SourceType: "official_pdf", SourceProvider: "arxiv", ExpectedMIME: "application/pdf"}
		if selected, err := arxivsource.Parse(arxivURL); err == nil {
			out.Candidate.URL, out.Candidate.Version = selected.PDFURL(), selected.Version
		}
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
		if e != nil || u.User != nil || (s.URL != "" && u.Scheme != "http" && u.Scheme != "https") {
			return out, nil
		}
		doi := NormalizeDOI(s.DOI)
		if u.Port() == "" && (strings.EqualFold(u.Hostname(), "doi.org") || strings.EqualFold(u.Hostname(), "dx.doi.org")) {
			doi = strings.ToLower(strings.TrimPrefix(u.Path, "/"))
		}
		if doiPattern.FindString(doi) != doi {
			doi = ""
		}
		var known orm.AcademicWork
		var linked orm.AcademicWorkDocument
		// A persisted acquisition is trusted evidence of the original locator.
		if doi != "" {
			e = db.WithContext(ctx).Where("doi_normalized = ?", doi).Take(&known).Error
		} else if s.URL == "" {
			return out, nil
		} else if e = db.WithContext(ctx).Where("source_locator = ?", s.URL).Take(&linked).Error; e == nil {
			e = db.WithContext(ctx).Where("id = ?", linked.AcademicWorkID).Take(&known).Error
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
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return out, e
		}
		candidates := candidatesForWork(known)
		var provenance map[string]any
		_ = json.Unmarshal(known.ProvenanceJSON, &provenance)
		hasTrustedPDF := len(candidates) > 0 && (known.ArxivIDBase != "" ||
			(known.DOINormalized != "" && provenance["trusted_fulltext"] == true))
		if !hasTrustedPDF {
			if doi == "" {
				return out, nil
			}
			out.Work = WorkInput{WorkID: known.ID, DOI: doi}
			out.PaperKey = "doi:" + doi
			out.NeedsPDFLookup = true
			out.Eligible, out.Reason = true, ""
			return out, nil
		}
		out.Work = WorkInput{WorkID: known.ID, Title: known.CanonicalTitle, DOI: known.DOINormalized, ArxivID: known.ArxivIDBase}
		out.PaperKey = "doi:" + known.DOINormalized
		if known.DOINormalized == "" {
			out.PaperKey = "arxiv:" + known.ArxivIDBase
		}
		out.Candidate = candidates[0]
		// Discovery may store a PDF hint before probing it. The provider and
		// source type are written by the existing verified PDF lookup paths.
		provider, _ := provenance["pdf_provider"].(string)
		kind, _ := provenance["pdf_source_type"].(string)
		out.PDFVerified = known.ArxivIDBase == "" && provider != "" && kind != ""
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
	return inspectSourceRecords(ctx, db, records, ids)
}

func inspectSourceRecords(ctx context.Context, db *gorm.DB, records []sourceRecord, ids []string) ([]SourceInspection, error) {
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
	items, e := inspectSourcePDFs(r.Context(), store.DB(), user, req.Context, req.IDs)
	if e != nil {
		replyError(w, 403, "SOURCE_UNAVAILABLE", e.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": items})
}

func LookupSourcePDFs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Context SourceContext `json:"source_context"`
		IDs     []string      `json:"source_ids"`
	}
	if !parseJSON(r, &req) || len(req.IDs) == 0 || len(req.IDs) > 24 {
		replyError(w, 400, "INVALID_REQUEST", "1–24 source_ids required")
		return
	}
	ctx, db := r.Context(), store.DB()
	items, err := inspectSourcePDFs(ctx, db, user, req.Context, req.IDs)
	if err != nil {
		replyError(w, 403, "SOURCE_UNAVAILABLE", err.Error())
		return
	}
	if err = lookupSourcePDFs(ctx, db, user, req.Context, items); err != nil {
		replyError(w, 500, "SOURCE_LOOKUP_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": items})
}

func lookupSourcePDFs(ctx context.Context, db *gorm.DB, user string, source SourceContext, items []SourceInspection) error {
	client, release := paperPDFClient(ctx)
	defer release()
	ctx = context.WithValue(ctx, paperPDFClientKey{}, client)
	var group errgroup.Group
	group.SetLimit(8)
	byDOI := map[string][]int{}
	for i, row := range items {
		if !row.Eligible || !row.NeedsPDFLookup {
			continue
		}
		if row.Candidate.URL != "" {
			group.Go(func() error {
				return checkSourcePDF(ctx, client, user, source, &items[i])
			})
		} else {
			byDOI[row.Work.DOI] = append(byDOI[row.Work.DOI], i)
		}
	}
	results := make([]paperPDFLookup, len(items))
	for doi, indices := range byDOI {
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			results[indices[0]] = lookupOpenDOIPDF(ctx, doi)
			return ctx.Err()
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for _, indices := range byDOI {
		found := results[indices[0]]
		if found.Candidate != nil {
			// Reuse the verified locator on submit without querying the DOI again.
			if err := rememberSourcePDF(ctx, db, items[indices[0]], *found.Candidate); err != nil {
				return err
			}
		}
		for _, i := range indices {
			items[i].NeedsPDFLookup = found.Candidate == nil
			items[i].PDFLookupFailed = found.Candidate == nil && (found.Err != nil || len(found.Warnings) > 0)
		}
	}
	return nil
}

func rememberSourcePDF(ctx context.Context, db *gorm.DB, source SourceInspection, candidate FulltextCandidate) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		work, err := sourceWork(ctx, tx, source)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&work, "id = ?", work.ID).Error; err != nil {
			return err
		}
		provenance := map[string]any{}
		if err := json.Unmarshal(work.ProvenanceJSON, &provenance); err != nil {
			return err
		}
		if len(candidatesForWork(work)) > 0 && (work.ArxivIDBase != "" || provenance["trusted_fulltext"] == true) {
			return nil
		}
		if provenance == nil {
			provenance = map[string]any{}
		}
		provenance["trusted_fulltext"], provenance["pdf_url"] = true, candidate.URL
		provenance["pdf_provider"], provenance["pdf_source_type"] = candidate.SourceProvider, candidate.SourceType
		provenance["pdf_license"], provenance["pdf_version"] = candidate.License, candidate.Version
		return tx.Model(&work).Update("metadata_provenance_json", marshal(provenance)).Error
	})
}
