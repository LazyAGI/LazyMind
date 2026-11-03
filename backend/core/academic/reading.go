package academic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
	"net/http"
	"sort"
	"strings"
	"time"
)

type paperReadReceipt struct {
	ContentHash   string    `json:"content_hash"`
	SourceVersion string    `json:"source_version"`
	Positions     []int     `json:"positions"`
	Pages         []int     `json:"pages"`
	Total         int       `json:"total_blocks"`
	Level         string    `json:"read_level"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func selectedContentPositions(pages []ContentPage, keywords []string, start, end int) ([]int, error) {
	if start < 0 || end < 0 || end < start || (start == 0 && end > 0) || len(keywords) > 12 {
		return nil, errors.New("invalid page range or keywords")
	}
	selected := map[int]bool{}
	if start > 0 {
		for i, p := range pages {
			if p.Page >= start && p.Page <= end {
				selected[i] = true
			}
		}
	} else if len(keywords) > 0 {
		for _, word := range keywords {
			if strings.TrimSpace(word) == "" || len(word) > 200 {
				return nil, errors.New("invalid keyword")
			}
		}
		for i, p := range pages {
			for _, word := range keywords {
				if strings.Contains(strings.ToLower(p.Text), strings.ToLower(word)) {
					for k := i - 1; k <= i+1; k++ {
						if k >= 0 && k < len(pages) {
							selected[k] = true
						}
					}
					break
				}
			}
		}
	} else {
		for i := range pages {
			selected[i] = true
		}
	}
	result := make([]int, 0, len(selected))
	for index := range selected {
		result = append(result, index)
	}
	sort.Ints(result)
	return result, nil
}
func recordPaperRead(ctx context.Context, user, expansion string, content orm.AcademicPaperContent, positions []int, pages []ContentPage) error {
	if expansion == "" || len(positions) == 0 {
		return nil
	}
	return conversationgroup.UserTransaction(ctx, store.DB(), user, func(tx *gorm.DB) error {
		var record orm.AcademicExpansion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", expansion, user).Take(&record).Error; err != nil {
			return err
		}
		ledger := map[string]paperReadReceipt{}
		if len(record.ReadingJSON) > 0 {
			if err := json.Unmarshal(record.ReadingJSON, &ledger); err != nil {
				return err
			}
		}
		entry := ledger[content.PaperID]
		if entry.ContentHash != content.ContentHash {
			entry = paperReadReceipt{ContentHash: content.ContentHash, SourceVersion: content.SourceVersion, Total: len(pages)}
		}
		seen := map[int]bool{}
		for _, p := range entry.Positions {
			seen[p] = true
		}
		for _, p := range positions {
			seen[p] = true
		}
		entry.Positions = []int{}
		entry.Pages = []int{}
		for p := range seen {
			entry.Positions = append(entry.Positions, p)
		}
		sort.Ints(entry.Positions)
		for _, p := range entry.Positions {
			entry.Pages = append(entry.Pages, pages[p].Page)
		}
		entry.Level = "related_sections"
		if len(entry.Positions) == len(pages) {
			entry.Level = "full"
		}
		entry.UpdatedAt = time.Now().UTC()
		ledger[content.PaperID] = entry
		return tx.Model(&record).Update("reading_json", marshal(ledger)).Error
	})
}

type paperReferenceClue struct {
	ReferenceKey     string    `json:"reference_key"`
	RawText          string    `json:"raw_text"`
	Title            string    `json:"title,omitempty"`
	Year             int       `json:"year,omitempty"`
	DOI              string    `json:"doi,omitempty"`
	ArxivID          string    `json:"arxiv_id,omitempty"`
	ResolutionStatus string    `json:"resolution_status"`
	Paper            *PaperRef `json:"paper,omitempty"`
	Error            string    `json:"error,omitempty"`
}

func readPaperReferences(w http.ResponseWriter, r *http.Request, user string, req paperContentRequest, source paperSource, row orm.AcademicPaperContent, pages []ContentPage, offset int) {
	if req.PageStart != 0 || req.PageEnd != 0 || req.Positions != nil || len(req.ReferenceKeys) > 5 || len(req.Keywords) > 5 || len(req.ReferenceKeys) > 0 && len(req.Keywords) > 0 || req.MaxResults < 0 || req.MaxResults > 20 {
		replyError(w, 400, "INVALID_REQUEST", "references accepts up to 5 reference_keys or keywords and max_results 1..20")
		return
	}
	for _, keyword := range req.Keywords {
		if strings.TrimSpace(keyword) == "" || len(keyword) > 200 {
			replyError(w, 400, "INVALID_REQUEST", "invalid keyword")
			return
		}
	}
	limit := req.MaxResults
	if limit == 0 {
		limit = 20
	}
	resolve := len(req.ReferenceKeys) > 0 || len(req.Keywords) > 0
	if resolve && limit > 5 {
		limit = 5
	}
	clues := []paperReferenceClue{}
	all := []extractedReference{}
	if usablePaperContent(row) {
		texts := make([]string, len(pages))
		for i, page := range pages {
			texts[i] = page.Text
		}
		all = splitReferences(bibliographySection(strings.Join(texts, "\n")))
	}
	selected := []extractedReference{}
	foundKeys := map[string]bool{}
	for _, item := range all {
		match := !resolve
		for _, key := range req.ReferenceKeys {
			if key == item.Key {
				match = true
				foundKeys[key] = true
			}
		}
		for _, keyword := range req.Keywords {
			if strings.Contains(strings.ToLower(item.Raw), strings.ToLower(keyword)) {
				match = true
			}
		}
		if match {
			selected = append(selected, item)
		}
	}
	if usablePaperContent(row) && len(foundKeys) != len(req.ReferenceKeys) {
		replyError(w, 400, "INVALID_REQUEST", "unknown or repeated reference_key")
		return
	}
	if offset > len(selected) {
		replyError(w, 400, "INVALID_CURSOR", "invalid reference offset")
		return
	}
	end := offset + limit
	if end > len(selected) {
		end = len(selected)
	}
	for _, item := range selected[offset:end] {
		clue := paperReferenceClue{ReferenceKey: item.Key, RawText: item.Raw, Title: citationTitle(item.Raw), Year: item.Year, DOI: item.DOI, ArxivID: item.Arxiv, ResolutionStatus: "unresolved"}
		if conflictingReferenceIDs(item.Raw) {
			clue.ResolutionStatus, clue.Error = "conflict", errConflictingPaperIdentifiers.Error()
		} else if resolve {
			paper, status, err := resolveReadReference(r.Context(), user, req, source, row, item)
			clue.ResolutionStatus = status
			if err != nil {
				clue.Error = err.Error()
			} else if paper.PaperID != "" {
				clue.Paper = &paper
			}
		}
		clues = append(clues, clue)
	}
	next := ""
	if end < len(selected) {
		next = base64.RawURLEncoding.EncodeToString(marshal(contentCursor{ID: row.ID, Hash: row.ContentHash, Offset: end, ExpansionID: req.ExpansionID, HandoffID: req.HandoffID, View: "references", ReferenceKeys: req.ReferenceKeys, Keywords: req.Keywords, MaxResults: limit}))
	}
	level := "none"
	if usablePaperContent(row) {
		level = "fulltext"
	}
	warnings := []string{}
	if row.Error != "" {
		warnings = append(warnings, row.Error)
	}
	if usablePaperContent(row) && len(all) == 0 {
		warnings = append(warnings, "未检测到原文参考文献条目")
	}
	common.ReplyJSON(w, map[string]any{"view": "references", "status": row.Status, "paper": source.Paper, "content_level": level, "content_hash": row.ContentHash, "references": clues, "total_references": len(all), "matched_references": len(selected), "next_cursor": next, "warnings": warnings, "parser_version": row.ParserVersion, "source_kind": row.SourceKind, "source_version": row.SourceVersion})
}

func resolveReadReference(ctx context.Context, user string, req paperContentRequest, source paperSource, content orm.AcademicPaperContent, item extractedReference) (PaperRef, string, error) {
	key := sourceHash(map[string]any{"content_hash": content.ContentHash, "paper_id": content.PaperID, "reference_key": item.Key, "extractor_version": extractorVersion})
	var record orm.AcademicExpansion
	if req.ExpansionID != "" {
		if err := store.DB().WithContext(ctx).Where("id=? AND user_id=? AND deleted_at IS NULL", req.ExpansionID, user).Take(&record).Error; err != nil {
			return PaperRef{}, "failed", err
		}
		var saved []paperDiscovery
		if err := json.Unmarshal(record.DiscoveryJSON, &saved); err != nil {
			return PaperRef{}, "failed", err
		}
		for _, entry := range saved {
			if entry.CacheKey == key && len(entry.Papers) > 0 {
				return entry.Papers[0], "resolved", nil
			}
		}
	}
	reference := orm.AcademicReference{RawText: item.Raw, Title: citationTitle(item.Raw), PublicationYear: item.Year, DOINormalized: item.DOI, ArxivIDBase: item.Arxiv, AuthorsJSON: marshal([]string{})}
	input, err := referenceMetadata(ctx, &reference, true)
	if err != nil {
		return PaperRef{}, "failed", err
	}
	if input.Provider == "" {
		return PaperRef{}, "unresolved", nil
	}
	version, _ := input.Provenance["pdf_version"].(string)
	url, _ := input.Provenance["paper_url"].(string)
	work := orm.AcademicWork{ArxivIDBase: input.ArxivID, ProvenanceJSON: marshal(input.Provenance)}
	candidates := candidatesForWork(work)
	if input.Provider == "arxiv" {
		url = "https://arxiv.org/abs/" + input.ArxivID + version
		for i := range candidates {
			candidates[i].Version = version
			candidates[i].URL = "https://arxiv.org/pdf/" + input.ArxivID + version
		}
	}
	if req.ExpansionID != "" {
		result, err := saveSearchPages(ctx, user, record, paperSearchRequest{Query: item.Raw, Mode: "references", PaperID: source.Paper.PaperID, MaxResults: 1, PreparationRevision: req.PreparationRevision}, []sourceSearchPage{{key: key, page: discoveredPage{Papers: []discoveredPaper{{Work: input, Version: version, PaperURL: url, Candidates: candidates}}}, outcome: searchOutcome{Source: input.Provider, Status: "ok", Count: 1}}})
		if err != nil {
			return PaperRef{}, "failed", err
		}
		if len(result.Papers) == 0 {
			return PaperRef{}, "conflict", errConflictingPaperIdentifiers
		}
		return result.Papers[0], "resolved", nil
	}
	// Reading handoffs have no expansion to attach candidates to. Retain only
	// verified identity metadata; no document or library entry is created.
	var registered orm.AcademicWork
	err = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; registered, _, e = findOrCreateWork(ctx, tx, input); return e })
	if err != nil {
		return PaperRef{}, "failed", err
	}
	paper := documentPaperRef(paperRef(registered), marshal(input))
	paper.SourceProvider, paper.SourceVersion, paper.PaperURL = input.Provider, version, url
	if len(candidates) > 0 {
		paper.PDFURL = candidates[0].URL
	}
	pinPaperURLs(&paper)
	return paper, "resolved", nil
}
