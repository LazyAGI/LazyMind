package academic

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/capability"
)

// SourceInspection deliberately omits its internal selection in public JSON.
// Persist it explicitly for operation replay without changing that public contract.
type paperImportDecision struct {
	Inspection SourceInspection           `json:"inspection"`
	Selection  *capability.PaperSelection `json:"selection,omitempty"`
}

// Both providers share a deadline; only metadata is fetched on the import path.
func resolveSelectedTitle(ctx context.Context, user string, p capability.PaperSelection) (WorkInput, error) {
	if strings.TrimSpace(p.Title) == "" {
		return WorkInput{}, errPaperSelectionUnconfirmed
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	type outcome struct {
		works []WorkInput
		err   error
	}
	results := make(chan outcome, 2)
	go func() {
		papers, _, err := externalPaperSearch(lookupCtx, user, "openalex", p.Title, 10)
		works := make([]WorkInput, 0, len(papers))
		for _, candidate := range papers {
			works = append(works, candidate.Work)
		}
		results <- outcome{works, err}
	}()
	go func() { works, err := searchCrossrefTitle(lookupCtx, p.Title); results <- outcome{works, err} }()
	matches := map[string]WorkInput{}
	failures := 0
	for i := 0; i < 2; i++ {
		var result outcome
		select {
		case result = <-results:
		case <-lookupCtx.Done():
			result.err = lookupCtx.Err()
		}
		if result.err != nil {
			failures++
			continue
		}
		for _, w := range result.works {
			if !selectionMetadataMatches(p, w.Title, w.Authors, w.Year) || !selectionVenueMatches(p.Venue, w.Venue) {
				continue
			}
			w.DOI = NormalizeDOI(w.DOI)
			if strings.HasPrefix(w.DOI, "10.48550/arxiv.") {
				if id, e := arxivsource.Parse(w.DOI); e == nil {
					w.ArxivID = id.BaseID
					w.DOI = ""
				}
			}
			key := ""
			if doiPattern.MatchString(w.DOI) {
				key = "doi:" + w.DOI
			} else if id, e := arxivsource.Parse(w.ArxivID); e == nil {
				w.ArxivID = id.BaseID
				key = "arxiv:" + id.BaseID
			}
			if key == "" {
				continue
			}
			if old, ok := matches[key]; ok {
				// Prefer Crossref deterministically for DOI metadata, fill omissions from the other provider.
				if w.Provider != "crossref" {
					old, w = w, old
				}
				if len(w.Authors) == 0 {
					w.Authors = old.Authors
				}
				if w.Year == 0 {
					w.Year = old.Year
				}
				if w.Venue == "" {
					w.Venue = old.Venue
				}
				if w.ArxivID == "" {
					w.ArxivID = old.ArxivID
				}
			}
			matches[key] = w
		}
	}
	if ctx.Err() != nil {
		return WorkInput{}, ctx.Err()
	}
	if len(matches) == 1 {
		for _, w := range matches {
			return w, nil
		}
	}
	options := []string{}
	for _, w := range matches {
		options = append(options, fmt.Sprintf("%s (%d, %s; %s)", w.Title, w.Year, w.Venue, firstNonEmpty(w.DOI, w.ArxivID)))
	}
	slices.Sort(options)
	if len(options) > 0 {
		return WorkInput{}, fmt.Errorf("论文候选需要确认（%s）：%w", strings.Join(options, " / "), errPaperSelectionUnconfirmed)
	}
	if failures > 0 {
		return WorkInput{}, fmt.Errorf("题录查询暂未完成（%d 个来源不可用），可重试", failures)
	}
	return WorkInput{}, fmt.Errorf("未找到题名明确匹配的论文，可补充 DOI/arXiv 或完整标题：%w", errPaperSelectionUnconfirmed)
}
