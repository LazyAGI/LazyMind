package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/asyncjob"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

var errPaperSelectionUnconfirmed = errors.New("无法唯一核对论文身份或指定版本")

func inspectPaperSelection(id string, p capability.PaperSelection) (SourceInspection, error) {
	rawDOI := p.DOI
	p.Title, p.WorkID = strings.TrimSpace(p.Title), strings.TrimSpace(p.WorkID)
	p.DOI, p.ArxivID = NormalizeDOI(p.DOI), strings.TrimSpace(p.ArxivID)
	out := SourceInspection{SourceID: id, Title: firstNonEmpty(p.Title, p.DOI, p.ArxivID, p.WorkID), Requested: &p}
	if strings.HasPrefix(p.DOI, "10.48550/arxiv.") {
		alias, err := arxivsource.Parse(rawDOI)
		if err != nil {
			return out, errPaperSelectionUnconfirmed
		}
		if p.ArxivID != "" {
			requested, err := arxivsource.Parse(p.ArxivID)
			if err != nil || requested.BaseID != alias.BaseID || requested.Version != "" && alias.Version != "" && requested.Version != alias.Version {
				return out, errPaperSelectionUnconfirmed
			}
			if requested.Version == "" {
				p.ArxivID = alias.ID()
			}
		} else {
			p.ArxivID = alias.ID()
		}
		p.DOI = ""
	}
	if p.DOI != "" && !doiPattern.MatchString(p.DOI) || len(p.Title) > 2000 || len(p.Authors) > 100 {
		return out, errPaperSelectionUnconfirmed
	}
	// Identity lookup happens in the worker; only syntax and local source links
	// are inspected while submitting a batch.
	key := ""
	if p.ArxivID != "" {
		a, err := arxivsource.Parse(p.ArxivID)
		if err != nil {
			return out, errPaperSelectionUnconfirmed
		}
		p.ArxivID = a.ID()
		p.ExactVersion = p.ExactVersion || a.Version != ""
		key = "arxiv:" + a.ID()
	}
	if p.DOI != "" && (key == "" || publicationRequested(p)) {
		key = "doi:" + p.DOI
	}
	if key == "" && p.WorkID != "" {
		key = "work:" + p.WorkID
	}
	if key == "" && p.Title != "" {
		key = "selection:" + sourceHash(p)
	}
	_, requestedVersion := NormalizeArxivID(p.ArxivID)
	if key == "" || p.ExactVersion && p.DOI == "" && requestedVersion == "" {
		return out, errPaperSelectionUnconfirmed
	}
	// Preserve the strictness of independently submitted selections.
	if p.ExactVersion {
		key += ":exact"
	}
	out.Eligible, out.PaperKey = true, key
	return out, nil
}

func publicationRequested(p capability.PaperSelection) bool {
	_, version := NormalizeArxivID(p.ArxivID)
	return p.ExactVersion && p.DOI != "" && version == ""
}

func selectionVenueMatches(wanted, actual string) bool {
	if wanted == "" {
		return true
	}
	normalized := func(value string) string {
		v := NormalizeTitle(value)
		if before, suffix, ok := strings.Cut(v, " "); ok && len(suffix) == 4 {
			if _, err := strconv.Atoi(suffix); err == nil {
				v = before
			}
		}
		if v == "neurips" || v == "nips" || v == "neural information processing systems" || strings.HasPrefix(v, "advances in neural information processing systems") {
			return "neurips"
		}
		return v
	}
	return normalized(wanted) == normalized(actual)
}

func selectionMetadataMatches(p capability.PaperSelection, title string, authors []string, year int) bool {
	if p.Title != "" && NormalizeTitle(p.Title) != NormalizeTitle(title) {
		return false
	}
	if p.Year != 0 && p.Year != year {
		return false
	}
	matched, specified := 0, 0
	for _, author := range p.Authors {
		wanted := strings.Fields(NormalizeTitle(author))
		if len(wanted) == 0 {
			continue
		}
		specified++
		if slices.ContainsFunc(authors, func(name string) bool {
			actual := strings.Fields(NormalizeTitle(name))
			return len(actual) > 0 && (strings.Join(wanted, " ") == strings.Join(actual, " ") ||
				wanted[len(wanted)-1] == actual[len(actual)-1] && (len(wanted) == 1 || wanted[0][0] == actual[0][0]))
		}) {
			matched++
		}
	}
	// A canonical ID, exact title and multiple matching coauthors can
	// corroborate a spelling difference in one author's name across editions.
	return matched == specified || p.Title != "" && (p.ArxivID != "" || p.DOI != "" || p.WorkID != "") && matched >= 3 && matched+1 == specified
}

func preferSelectedPublication(p *capability.PaperSelection) bool {
	return p != nil && (publicationRequested(*p) || p.DOI == "" && p.ArxivID == "" && p.WorkID == "" && p.Venue != "")
}

func lookupSelectedDOIPDF(ctx context.Context, user, doi, title string, p *capability.PaperSelection) paperPDFLookup {
	if preferSelectedPublication(p) {
		found := lookupPublishedPDF(ctx, user, doi, title)
		if found.Candidate != nil || publicationRequested(*p) {
			return found
		}
	}
	return lookupOpenDOIPDF(ctx, doi)
}

func resolveSelectedTitle(ctx context.Context, user string, p capability.PaperSelection) (WorkInput, error) {
	papers, _, err := externalPaperSearch(ctx, user, "openalex", p.Title, 10)
	if err != nil {
		return WorkInput{}, err
	}
	byIdentity := map[string]WorkInput{}
	for _, candidate := range papers {
		w := candidate.Work
		if !selectionMetadataMatches(p, w.Title, w.Authors, w.Year) || !selectionVenueMatches(p.Venue, w.Venue) {
			continue
		}
		key := firstNonEmpty(NormalizeDOI(w.DOI), w.ArxivID)
		if key != "" {
			byIdentity[key] = w
		}
	}
	if len(byIdentity) != 1 {
		var options []string
		for _, w := range byIdentity {
			options = append(options, fmt.Sprintf("%s (%d, %s; %s)", w.Title, w.Year, w.Venue, firstNonEmpty(w.DOI, w.ArxivID)))
		}
		slices.Sort(options)
		if len(options) > 0 {
			return WorkInput{}, fmt.Errorf("%w：%s", errPaperSelectionUnconfirmed, strings.Join(options, " / "))
		}
		return WorkInput{}, errPaperSelectionUnconfirmed
	}
	for _, w := range byIdentity {
		return w, nil
	}
	return WorkInput{}, errPaperSelectionUnconfirmed
}

func resolveRequestedPaper(ctx context.Context, caller string, p capability.PaperSelection) (WorkInput, error) {
	if p.WorkID != "" {
		var known orm.AcademicWork
		if err := store.DB().WithContext(ctx).Where("id=?", p.WorkID).Take(&known).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return WorkInput{}, errPaperSelectionUnconfirmed
			}
			return WorkInput{}, err
		}
		ref := paperRef(known)
		arxiv, _ := NormalizeArxivID(p.ArxivID)
		if p.DOI != "" && p.DOI != ref.DOI || arxiv != "" && arxiv != ref.ArxivID || !selectionMetadataMatches(p, ref.Title, ref.Authors, ref.Year) {
			return WorkInput{}, errPaperSelectionUnconfirmed
		}
		return WorkInput{WorkID: known.ID, DOI: ref.DOI, ArxivID: ref.ArxivID, Title: ref.Title, Authors: ref.Authors, Year: ref.Year}, nil
	}
	if p.DOI != "" {
		input, err := lookupPaperLinkMetadata(ctx, p.DOI)
		if err != nil {
			return WorkInput{}, err
		}
		if !selectionMetadataMatches(p, input.Title, input.Authors, input.Year) {
			return WorkInput{}, errPaperSelectionUnconfirmed
		}
		if p.ArxivID != "" {
			// A DOI and an arXiv identifier supplied together must have a
			// corroborated relationship before either can select the original.
			meta, err := lookupImportArxiv(ctx, p.ArxivID)
			if err != nil {
				return WorkInput{}, err
			}
			if NormalizeDOI(meta.DOI) != p.DOI {
				return WorkInput{}, errPaperSelectionUnconfirmed
			}
			if meta.Version != "" && p.ExactVersion && !publicationRequested(p) {
				return arxivWorkInput(meta), nil
			}
			input.ArxivID = meta.BaseID
		}
		return input, nil
	}
	if p.ArxivID != "" {
		meta, err := lookupImportArxiv(ctx, p.ArxivID)
		if err != nil {
			return WorkInput{}, err
		}
		if !selectionMetadataMatches(p, meta.Title, meta.Authors, meta.Year) {
			return WorkInput{}, errPaperSelectionUnconfirmed
		}
		return arxivWorkInput(meta), nil
	}
	return resolveSelectedTitle(ctx, caller, p)
}

var arxivSourceAdmission = make(chan struct{}, 1)

func withArxivSource[T any](ctx context.Context, request func() (T, error)) (T, error) {
	var zero T
	select {
	case arxivSourceAdmission <- struct{}{}:
		defer func() { <-arxivSourceAdmission }()
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	scope := sourceHash(map[string]any{"source": "arxiv", "key": "", "tools": map[string]any(nil)})
	if store.State() != nil {
		cooldown, err := searchCooldown(ctx, scope)
		if err != nil {
			return zero, err
		}
		if cooldown != nil {
			return zero, cooldown
		}
	}
	result, err := request()
	if err != nil && store.State() != nil {
		failure := classifySearchFailure(err)
		if failure.Status == "rate_limited" {
			if e := saveSearchCooldown(ctx, scope, failure); e != nil {
				return zero, e
			}
			return zero, failure
		}
	}
	return result, err
}

func lookupImportArxiv(ctx context.Context, id string) (arxivsource.Metadata, error) {
	return withArxivSource(ctx, func() (arxivsource.Metadata, error) { return arxivsource.NewClient().Lookup(ctx, id) })
}

func paperSelectionFromSource(ctx context.Context, db *gorm.DB, paper capability.PaperSelection, records []sourceRecord) (capability.PaperSelection, error) {
	if paper.SourceID == "" {
		return paper, nil
	}
	for _, record := range records {
		if sourceRecordID(record) != paper.SourceID {
			continue
		}
		row, err := inspectRecord(ctx, db, record)
		if err != nil {
			return paper, err
		}
		if paper.Title == "" {
			paper.Title = paperRecordTitle(row.Title, row.Work.ArxivID)
		}
		if paper.WorkID == "" && paper.DOI == "" && paper.ArxivID == "" && row.Eligible {
			paper.WorkID, paper.DOI = row.Work.WorkID, row.Work.DOI
			paper.ArxivID = row.Work.ArxivID
			if paper.ExactVersion && row.Candidate.Version != "" {
				paper.ArxivID += row.Candidate.Version
			}
		}
		return paper, nil
	}
	return paper, errPaperSelectionUnconfirmed
}

func paperRecordTitle(title, id string) string {
	title = strings.TrimSpace(title)
	if id != "" {
		title = strings.TrimSpace(strings.TrimPrefix(title, "["+id+"]"))
	}
	return title
}

func savedArxivSelection(ctx context.Context, batch orm.PaperImportBatch, p capability.PaperSelection) (bool, error) {
	source := sourcePolicy(batch).Context
	if source.ConversationID == "" || p.ArxivID == "" || p.Title == "" || len(p.Authors) == 0 {
		return false, nil
	}
	a, e := arxivsource.Parse(p.ArxivID)
	if e != nil {
		return false, e
	}
	records, e := loadSourceRecords(ctx, store.DB(), batch.CreatedBy, source)
	if e != nil {
		// A current-turn source can be enqueued before its answer is persisted.
		return false, nil
	}
	for _, r := range records {
		if officialArxivID(r.URL) == a.BaseID && NormalizeTitle(paperRecordTitle(r.Title, a.BaseID)) == NormalizeTitle(p.Title) {
			return true, nil
		}
	}
	return false, nil
}

func bindRequestedWork(ctx context.Context, job asyncjob.Job, batch orm.PaperImportBatch, item *orm.PaperImportItem, input WorkInput, selected savedImportCandidate) error {
	return store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := sourceJobFence(tx, job, batch.ID); err != nil {
			return err
		}
		work, _, err := findOrCreateWork(ctx, tx, input)
		if err != nil {
			return err
		}
		item.AcademicWorkID = work.ID
		if selected.VerifiedPaper != nil {
			selected.VerifiedPaper.PaperID = work.ID
		}
		item.SelectedCandidateJSON = marshal(selected)
		return tx.Model(item).Updates(map[string]any{"academic_work_id": work.ID, "selected_candidate_json": item.SelectedCandidateJSON, "updated_at": time.Now().UTC()}).Error
	})
}

func resolvePaperImportSelection(ctx context.Context, job asyncjob.Job, batch orm.PaperImportBatch, item *orm.PaperImportItem, selected savedImportCandidate, originals *doc.OriginalService) error {
	p := *selected.Requested
	if err := sourceItemUpdate(ctx, job, batch, item.ID, map[string]any{"stage": "resolve"}); err != nil {
		return err
	}
	// A saved official result plus the independently verified original can
	// establish an unversioned identity without calling the arXiv API again.
	if !p.ExactVersion && p.DOI == "" && p.WorkID == "" {
		trusted, err := savedArxivSelection(ctx, batch, p)
		if err != nil {
			return err
		}
		if trusted {
			id, _ := arxivsource.Parse(p.ArxivID)
			ref := PaperRef{Title: p.Title, Authors: p.Authors, Year: p.Year, ArxivID: id.BaseID}
			candidate, err := conversationOriginalCandidate(ctx, batch, ref, "", false, originals)
			if err != nil {
				return err
			}
			if candidate.ConversationOriginal != nil {
				candidate.Requested = &p
				return bindRequestedWork(ctx, job, batch, item, WorkInput{Title: p.Title, Authors: p.Authors, Year: p.Year, ArxivID: id.BaseID}, candidate)
			}
		}
	}
	input, err := resolveRequestedPaper(ctx, batch.CreatedBy, p)
	if err != nil {
		return err
	}
	ref := PaperRef{Title: input.Title, Authors: input.Authors, Year: input.Year, Venue: input.Venue, DOI: input.DOI, ArxivID: input.ArxivID}
	if input.Abstract != "" {
		ref.Abstract = &input.Abstract
	}
	// Keep the request alongside the resolved candidate so retries preserve it.
	next := savedImportCandidate{Requested: &p}
	version, _ := input.Provenance["pdf_version"].(string)
	pdfURL, _ := input.Provenance["pdf_url"].(string)
	if !publicationRequested(p) && input.Provider == "arxiv" && version != "" && pdfURL != "" {
		ref.SourceVersion, ref.SourceProvider, ref.PDFURL = version, "arxiv", pdfURL
		next.FulltextCandidate = FulltextCandidate{URL: pdfURL, SourceProvider: "arxiv", Version: version, SourceType: "official_pdf", ExpectedMIME: "application/pdf"}
		next.VerifiedPaper = &ref
	}
	if !publicationRequested(p) && input.ArxivID != "" && next.URL == "" {
		lookupID := firstNonEmpty(p.ArxivID, input.ArxivID)
		id, e := arxivsource.Parse(lookupID)
		if e != nil {
			return e
		}
		next.FulltextCandidate = FulltextCandidate{URL: id.PDFURL(), SourceProvider: "arxiv", Version: id.Version, SourceType: "official_pdf", ExpectedMIME: "application/pdf"}
	}
	if input.DOI != "" && next.URL == "" {
		// DOI acquisition is deferred until after presence and original reuse.
		next.VerifiedPaper = &ref
	}
	return bindRequestedWork(ctx, job, batch, item, input, next)
}

func prepareConversationImportCandidate(ctx context.Context, job asyncjob.Job, batch orm.PaperImportBatch, item *orm.PaperImportItem, selected savedImportCandidate, originals *doc.OriginalService) error {
	db := store.DB().WithContext(ctx)
	var work orm.AcademicWork
	version, provider := selected.Version, selected.SourceProvider
	exactDOI := false
	if p := selected.Requested; p != nil {
		if publicationRequested(*p) {
			version, provider, exactDOI = "publishedVersion", "", true
		} else if id, e := arxivsource.Parse(p.ArxivID); e == nil && id.Version != "" {
			version, provider = id.Version, "arxiv"
		} else if !p.ExactVersion {
			version, provider = "", ""
		}
		if item.AcademicWorkID == "" {
			q := db
			switch {
			case p.WorkID != "":
				q = q.Where("id=?", p.WorkID)
			case p.DOI != "":
				q = q.Where("doi_normalized=?", p.DOI)
			case p.ArxivID != "":
				a, _ := arxivsource.Parse(p.ArxivID)
				q = q.Where("arxiv_id_base=?", a.BaseID)
			default:
				q = nil
			}
			if q != nil {
				if e := q.Take(&work).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
					return e
				}
				if work.ID != "" {
					ref := paperRef(work)
					a, _ := arxivsource.Parse(p.ArxivID)
					if p.DOI != "" && ref.DOI != p.DOI || a.BaseID != "" && ref.ArxivID != a.BaseID ||
						!selectionMetadataMatches(*p, ref.Title, ref.Authors, ref.Year) {
						work = orm.AcademicWork{}
					}
				}
			}
		}
	}
	checkPresence := func(workID string) (bool, error) {
		if workID == "" {
			return false, nil
		}
		present, d, err := findEquivalentPresentWork(ctx, workID, batch.TargetDatasetID, true, version, provider)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err = completePresentImport(ctx, job, batch, *item, present.ID, d.DocumentID); err != nil {
			return false, err
		}
		item.Stage = "done"
		return true, nil
	}
	if found, err := checkPresence(firstNonEmpty(item.AcademicWorkID, work.ID)); found || err != nil {
		return err
	}
	if selected.Requested != nil && item.AcademicWorkID == "" {
		if err := resolvePaperImportSelection(ctx, job, batch, item, selected, originals); err != nil {
			return err
		}
		if err := json.Unmarshal(item.SelectedCandidateJSON, &selected); err != nil {
			return err
		}
		if found, err := checkPresence(item.AcademicWorkID); found || err != nil {
			return err
		}
	}
	if selected.ConversationOriginal != nil {
		return nil
	}
	if err := db.Where("id=?", item.AcademicWorkID).Take(&work).Error; err != nil {
		return err
	}
	ref := paperRef(work)
	ref.Title = paperRecordTitle(ref.Title, ref.ArxivID)
	if selected.VerifiedPaper != nil {
		ref = *selected.VerifiedPaper
	}
	if selected.Requested != nil {
		p := selected.Requested
		if len(ref.Authors) == 0 && len(p.Authors) > 0 {
			ref.Authors = p.Authors
		}
		if p.DOI != "" && ref.DOI == "" {
			ref.DOI = p.DOI
		}
	}
	cached, err := conversationOriginalCandidate(ctx, batch, ref, version, exactDOI, originals)
	if err != nil {
		return err
	}
	if cached.ConversationOriginal != nil {
		cached.Requested = selected.Requested
		item.SelectedCandidateJSON = marshal(cached)
		return sourceItemUpdate(ctx, job, batch, item.ID, map[string]any{"selected_candidate_json": item.SelectedCandidateJSON})
	}
	// Reuse a DOI locator already verified by the existing source lookup.
	if selected.URL == "" && ref.DOI != "" && len(work.ProvenanceJSON) != 0 && !preferSelectedPublication(selected.Requested) {
		var provenance struct {
			Trusted bool `json:"trusted_fulltext"`
		}
		if err := json.Unmarshal(work.ProvenanceJSON, &provenance); err != nil {
			return err
		}
		if provenance.Trusted {
			for _, candidate := range candidatesForWork(work) {
				if candidate.SourceProvider == "arxiv" || exactDOI && candidate.Version != "publishedVersion" {
					continue
				}
				ref.SourceProvider, ref.SourceVersion, ref.PDFURL = candidate.SourceProvider, candidate.Version, candidate.URL
				selected.FulltextCandidate, selected.VerifiedPaper = candidate, &ref
				item.SelectedCandidateJSON = marshal(selected)
				return sourceItemUpdate(ctx, job, batch, item.ID, map[string]any{"selected_candidate_json": item.SelectedCandidateJSON})
			}
		}
	}
	return nil
}
