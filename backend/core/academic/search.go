package academic

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type paperSearchResponse struct {
	Papers   []PaperRef      `json:"papers"`
	Sources  []searchOutcome `json:"sources"`
	Leads    []discoveryLead `json:"leads"`
	CacheHit bool            `json:"cache_hit"`
	Mock     bool            `json:"mock"`
}

type sourceSearchPage struct {
	request paperSearchRequest
	key     string
	page    discoveredPage
	outcome searchOutcome
	cached  *paperDiscovery
}

func fetchSearchPages(ctx context.Context, user string, record orm.AcademicExpansion, req paperSearchRequest, previous []paperDiscovery) ([]sourceSearchPage, bool, error) {
	pages := make([]sourceSearchPage, 0, len(req.Sources))
	cacheHit := true
	for _, source := range req.Sources {
		request := req
		request.Sources = []string{source}
		entry := sourceSearchPage{request: request}
		if err := configureSearchSource(ctx, user, source, &entry.request); err != nil {
			var failure *searchFailure
			if !errors.As(err, &failure) {
				return nil, false, err
			}
			entry.outcome = failureOutcome(source, failure)
			cacheHit = false
		} else {
			entry.key = sourceHash(map[string]any{"query": req.Query, "mode": req.Mode, "paper_id": req.PaperID, "limit": req.MaxResults, "position": req.position, "scope": entry.request.scope, "preparation_revision": record.PreparationRevision})
			for i := len(previous) - 1; i >= 0; i-- {
				saved := &previous[i]
				if saved.CacheKey == entry.key && len(saved.Sources) == 1 && (saved.Sources[0].Status == "ok" || saved.Sources[0].Status == "empty") {
					entry.cached = saved
					entry.outcome = saved.Sources[0]
					entry.outcome.Reused = true
					entry.outcome.NewPaperCount = 0
					entry.outcome.ElapsedMS = 0
					break
				}
			}
			if entry.cached == nil {
				cacheHit = false
				var err error
				entry.page, entry.outcome, err = fetchPaperPage(ctx, record, source, entry.request)
				if err != nil {
					return nil, false, err
				}
				if entry.page.Next != nil {
					entry.outcome.NextCursor = nextSearchCursor(record, entry.key)
				}
			}
		}
		pages = append(pages, entry)
	}
	return pages, cacheHit, nil
}

func saveSearchPages(ctx context.Context, user string, record orm.AcademicExpansion, req paperSearchRequest, pages []sourceSearchPage) (paperSearchResponse, error) {
	refs := []PaperRef{}
	leads := []discoveryLead{}
	outcomes := []searchOutcome{}
	seen := map[string]bool{}
	revision := record.PreparationRevision
	err := store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", record.ID, user).Take(&record).Error; err != nil {
			return err
		}
		if record.DiscoveryMode != "external" {
			return errors.New("unsupported expansion discovery mode")
		}
		if record.PreparationRevision != revision {
			return errors.New("research scope changed during search")
		}
		if req.PreparationRevision != nil && (record.PreparationRevision != *req.PreparationRevision || record.Status != "preparing") {
			return errors.New("preparation attempt is no longer current")
		}
		var searches []paperDiscovery
		if err := json.Unmarshal(record.DiscoveryJSON, &searches); err != nil {
			return err
		}
		known := map[string]PaperRef{}
		for _, search := range searches {
			for _, paper := range search.Papers {
				if _, exists := known[paper.PaperID]; !exists {
					known[paper.PaperID] = paper
				}
			}
		}
		changed := false
		for _, entry := range pages {
			saved := paperDiscovery{PreparationRevision: record.PreparationRevision, CacheKey: entry.key, MaxResults: req.MaxResults, Position: req.position, Next: entry.page.Next, Query: req.Query, CreatedAt: time.Now().UTC(), Mode: req.Mode, PaperID: req.PaperID, Papers: []PaperRef{}, Leads: entry.page.Leads, Candidates: map[string][]FulltextCandidate{}}
			if entry.cached != nil {
				saved = *entry.cached
			} else {
				pageSeen := map[string]bool{}
				for _, metadata := range entry.page.Papers {
					work, _, err := findOrCreateWork(ctx, tx, metadata.Work)
					if errors.Is(err, errConflictingPaperIdentifiers) {
						saved.Leads = append(saved.Leads, discoveryLead{metadata.Work.Title, metadata.PaperURL, "强标识冲突，需核对后才能归并"})
						continue
					}
					if err != nil {
						return err
					}
					ref := paperRef(work)
					ref.SourceVersion, ref.SourceProvider = metadata.Version, metadata.Work.Provider
					ref.PaperURL = metadata.PaperURL
					if len(metadata.Candidates) > 0 {
						ref.PDFURL = metadata.Candidates[0].URL
					}
					saved.Candidates[work.ID] = append(saved.Candidates[work.ID], metadata.Candidates...)
					pinPaperURLs(&ref)
					if prior, exists := known[ref.PaperID]; exists {
						ref = prior
					} else {
						known[ref.PaperID] = ref
						entry.outcome.NewPaperCount++
					}
					if !pageSeen[ref.PaperID] {
						saved.Papers = append(saved.Papers, ref)
						pageSeen[ref.PaperID] = true
					}
				}
				var session orm.WorkflowSession
				if err := tx.Where("conversation_id=?", record.ConversationID).Order("created_at DESC").Take(&session).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				saved.SessionID = session.ID
				entry.outcome.PaperCount, entry.outcome.LeadCount = len(saved.Papers), len(saved.Leads)
				saved.Sources = []searchOutcome{entry.outcome}
				searches = append(searches, saved)
				changed = true
			}
			for _, ref := range saved.Papers {
				if !seen[ref.PaperID] {
					refs = append(refs, ref)
					seen[ref.PaperID] = true
				}
			}
			leads = append(leads, saved.Leads...)
			outcomes = append(outcomes, entry.outcome)
		}
		if changed {
			return tx.Model(&record).Update("discovery_json", marshal(searches)).Error
		}
		return nil
	})
	if err != nil {
		return paperSearchResponse{}, err
	}
	return paperSearchResponse{Papers: refs, Sources: outcomes, Leads: leads}, nil
}

// Provider failures are observable partial results; state-store failures remain errors.
func fetchPaperPage(ctx context.Context, record orm.AcademicExpansion, source string, request paperSearchRequest) (discoveredPage, searchOutcome, error) {
	failure, err := searchCooldown(ctx, request.scope)
	if err != nil {
		return discoveredPage{}, searchOutcome{}, err
	}
	if failure != nil {
		outcome := failureOutcome(source, failure)
		outcome.Reused = true
		return discoveredPage{}, outcome, nil
	}
	start := time.Now()
	page, err := discoverPaperPage(ctx, record, source, request)
	var outcome searchOutcome
	if err != nil {
		failure = classifySearchFailure(err)
		if err := saveSearchCooldown(ctx, request.scope, failure); err != nil {
			return discoveredPage{}, searchOutcome{}, err
		}
		outcome = failureOutcome(source, failure)
	} else {
		outcome = searchOutcome{Source: source, Status: "ok", Count: len(page.Papers) + len(page.Leads), PaperCount: len(page.Papers), LeadCount: len(page.Leads), TotalCount: page.TotalCount, HasMore: page.Next != nil}
		if outcome.Count == 0 {
			outcome.Status = "empty"
		}
	}
	outcome.ElapsedMS = time.Since(start).Milliseconds()
	return page, outcome, nil
}
