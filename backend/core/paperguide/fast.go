package paperguide

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
)

// ExplainerPlan is the deliberately small model contract, independent of the
// historical claims/review pipeline. Public artifacts retain the existing DTOs.
type ExplainerPlan struct {
	PaperFlow      *GuideFlow             `json:"paper_flow,omitempty"`
	ReaderProfile  string                 `json:"reader_profile,omitempty"`
	SharedExamples []ExplainerExample     `json:"shared_examples,omitempty"`
	Title          string                 `json:"title"`
	Subtitle       string                 `json:"subtitle"`
	Chapters       []ExplainerChapterPlan `json:"chapters"`
}
type ExplainerExampleInput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Unit  string `json:"unit"`
}
type ExplainerExample struct {
	ID            string                  `json:"id"`
	Scenario      string                  `json:"scenario"`
	Inputs        []ExplainerExampleInput `json:"inputs"`
	Relationships []string                `json:"relationships"`
}
type ExplainerFigure struct {
	ID      string `json:"id"`
	Page    int    `json:"page"`
	Caption string `json:"caption"`
	Section string `json:"section"`
}

type ExplainerChapterPlan struct {
	KnowledgeGaps      []string `json:"knowledge_gaps,omitempty"`
	ReasoningQuestions []string `json:"reasoning_questions,omitempty"`
	ExampleIDs         []string `json:"example_ids,omitempty"`
	FigureIDs          []string `json:"figure_ids,omitempty"`
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Purpose            string   `json:"purpose"`
	Outline            []string `json:"outline"`
	Interaction        string   `json:"interaction"`
	SourceIDs          []string `json:"source_ids"`
}
type ExplainerChapter struct {
	ID     string           `json:"id"`
	Title  string           `json:"title"`
	Blocks []ExplainerBlock `json:"blocks"`
}
type ExplainerBlock struct {
	Emphasis    string   `json:"emphasis,omitempty"`
	FigureID    string   `json:"figure_id,omitempty"`
	Kind        string   `json:"kind"`
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
	HTML        string   `json:"html"`
}

var chapterSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validateExplainerPlan(p ExplainerPlan) error {
	if strings.TrimSpace(p.Title) == "" || len(p.Chapters) < 8 || len(p.Chapters) > 12 {
		return ErrEvidence
	}
	backbone := []string{"overview", "why-it-matters", "big-idea", "results", "limitations", "glossary"}
	examples := map[string]bool{}
	for _, e := range p.SharedExamples {
		if !chapterSlug.MatchString(e.ID) || examples[e.ID] {
			return ErrEvidence
		}
		examples[e.ID] = true
	}
	next := 0
	seen := map[string]bool{}
	for _, c := range p.Chapters {
		for _, id := range c.ExampleIDs {
			if !examples[id] {
				return ErrEvidence
			}
		}
		if !chapterSlug.MatchString(c.ID) || seen[c.ID] || strings.TrimSpace(c.Title) == "" {
			return ErrEvidence
		}
		seen[c.ID] = true
		if next < len(backbone) && c.ID == backbone[next] {
			next++
		}
	}
	results := -1
	for i, c := range p.Chapters {
		if c.ID == "results" {
			results = i
		}
	}
	n := len(p.Chapters)
	if p.Chapters[0].ID != "overview" || p.Chapters[1].ID != "why-it-matters" || p.Chapters[2].ID != "big-idea" || p.Chapters[n-2].ID != "limitations" || p.Chapters[n-1].ID != "glossary" || results-3 < 2 || results-3 > 4 || n-results-3 < 0 || n-results-3 > 2 {
		return ErrEvidence
	}
	if next != len(backbone) {
		return ErrEvidence
	}
	return nil
}
func validateExplainerChapter(c ExplainerChapter, id string) error {
	if c.ID != id || strings.TrimSpace(c.Title) == "" || len(c.Blocks) == 0 || len(c.Blocks) > 40 {
		return ErrEvidence
	}
	for _, b := range c.Blocks {
		if strings.TrimSpace(b.Text) == "" {
			return ErrEvidence
		}
		if b.Kind != "figure" && b.FigureID != "" {
			return ErrEvidence
		}
		switch b.Kind {
		case "figure":
			if b.FigureID == "" || b.HTML != "" {
				return ErrEvidence
			}
		case "paragraph", "heading", "formula":
			if b.HTML != "" {
				return ErrEvidence
			}
		case "playground":
			if strings.TrimSpace(b.HTML) == "" || len(b.HTML) > 160000 {
				return ErrEvidence
			}
		default:
			return ErrEvidence
		}
	}
	return nil
}

// Model calls overlap; transactions do not. This also avoids SQLite writer
// contention, while each transaction still enforces the durable lease/epoch.
type serialCheckpointReporter struct {
	asyncjob.FencedReporter
	mu sync.Mutex
}

func (f *serialCheckpointReporter) WithLease(ctx context.Context, fn func(*gorm.DB) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.FencedReporter.WithLease(ctx, fn)
}

func (x *execution) executeFast(ctx context.Context) error {
	if err := x.prepare(ctx); err != nil {
		return err
	}
	x.f = &serialCheckpointReporter{FencedReporter: x.f}
	input, aliases, err := x.explainerInput()
	if err != nil {
		return err
	}
	if err = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "planning", ""); err != nil {
		return err
	}
	var plan ExplainerPlan
	if err = x.task(ctx, "fast:plan", "paper.explainer.plan", input, &plan, func() error {
		if e := validateExplainerPlan(plan); e != nil {
			return e
		}
		return validateExplainerFigures(plan, nil, input["figures"].([]ExplainerFigure))
	}); err != nil {
		return err
	}
	public := GuidePlan{PaperFlow: plan.PaperFlow, Layout: "flashpapers", Title: plan.Title, Subtitle: plan.Subtitle, Questions: []string{}, Claims: []GuideClaim{}, TermPolicy: []string{}, RequiredAssets: []GuideAssetDecision{}, Sections: []GuideSectionPlan{}}
	for _, c := range plan.Chapters {
		public.Sections = append(public.Sections, GuideSectionPlan{Role: Role(c.ID), Title: c.Title, Focus: c.Purpose, Terms: c.Outline, ClaimIDs: []string{}, EvidenceIDs: canonicalReferences(c.SourceIDs, aliases), AssetIDs: canonicalReferences(c.FigureIDs, aliases)})
	}
	if err = x.s.Repo.PutStep(ctx, x.f, x.rev.ID, x.rev.Epoch, StepWrite{Key: "plan", InputHash: digest(plan), Output: mustMarshal(public)}); err != nil {
		return err
	}
	if err = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "writing", ""); err != nil {
		return err
	}
	concurrency := x.s.ChapterConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	concurrency = min(concurrency, 8)
	work := make(chan ExplainerChapterPlan, len(plan.Chapters))
	for _, c := range plan.Chapters {
		work <- c
	}
	close(work)
	failures := make(chan error, len(plan.Chapters))
	var wg sync.WaitGroup
	started := time.Now()
	// Each worker owns its checkpoint map and timing counters. Only immutable
	// snapshot/model/plan values are shared. Failed chapters do not discard peers.
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := *x
			worker.steps = maps.Clone(x.steps)
			worker.started = time.Time{}
			worker.callTime = 0
			worker.overheadReserved = false
			for c := range work {
				if ctx.Err() != nil {
					failures <- ctx.Err()
					continue
				}
				if e := worker.s.Repo.PutStep(ctx, worker.f, worker.rev.ID, worker.rev.Epoch, StepWrite{Key: fmt.Sprintf("chapter-start:%d:%s", worker.rev.Epoch, c.ID), InputHash: c.ID, Output: mustMarshal(c.ID)}); e != nil {
					failures <- e
					continue
				}
				chapterInput, chapterAliases := worker.chapterInput(input, plan, c, aliases)
				var chapter ExplainerChapter
				e := worker.task(ctx, "fast:chapter:"+c.ID, "paper.explainer.chapter", chapterInput, &chapter, func() error {
					if e := validateExplainerChapter(chapter, c.ID); e != nil {
						return e
					}
					return validateExplainerFigures(ExplainerPlan{}, &chapter, chapterInput["figures"].([]ExplainerFigure))
				})
				if e == nil {
					section := publicChapter(chapter, chapterAliases)
					if _, e = worker.check(ctx); e == nil {
						e = worker.s.Repo.PutStep(ctx, worker.f, worker.rev.ID, worker.rev.Epoch, StepWrite{Key: sectionKey(section.Role, 1), InputHash: digest(chapter), Output: mustMarshal(section), Published: true})
					}
				}
				if e != nil {
					failures <- e
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	// Budget model durations are settled per request. Overhead excludes this
	// parallel wall-time interval, never subtracting a sum of overlapping calls.
	x.callTime += time.Since(started).Milliseconds()
	for e := range failures {
		if err == nil {
			err = e
		}
	}
	if err != nil {
		return err
	}
	if _, err = x.check(ctx); err != nil {
		return err
	}
	if err = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "checking", ""); err != nil {
		return err
	}
	if err = x.settleOverhead(ctx); err != nil {
		return err
	}
	return x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "completed", "", "complete")
}
func (x *execution) chapterInput(input map[string]any, plan ExplainerPlan, chapter ExplainerChapterPlan, aliases map[string]string) (map[string]any, map[string]string) {
	chapterInput := maps.Clone(input)
	chapterInput["plan"] = plan
	chapterInput["chapter_id"] = chapter.ID
	figures := input["figures"].([]ExplainerFigure)
	// Preserve every request that could already have produced a checkpoint.
	// When the shared plan pushes a chapter over budget, its writer only
	// needs the full captions of figures assigned to this chapter.
	if x.inputFits(chapterInput) {
		return chapterInput, aliases
	}
	if len(figures) > 0 {
		selected := make([]ExplainerFigure, 0, len(chapter.FigureIDs))
		for _, figure := range figures {
			if slices.Contains(chapter.FigureIDs, figure.ID) {
				selected = append(selected, figure)
			}
		}
		chapterInput["figures"] = selected
	}
	// Keep the exact excerpts selected during planning, including their stable
	// aliases and page markers. Reserve assigned evidence before optional context.
	source := input["source_text"].(string)
	chapterInput["source_text"] = ""
	header, _ := json.MarshalIndent(chapterInput, "", "")
	chapterInput["source_text"] = source
	remaining := min(1<<20, x.modelInputBudget()-6000) - len(header)
	whole, _ := json.Marshal(source)
	if len(whole)-2 <= remaining {
		return chapterInput, aliases
	}
	selected := make([]bool, len(x.excerpts))
	costs := make([]int, len(x.excerpts))
	start := 0
	for i, excerpt := range x.excerpts {
		encoded, _ := json.Marshal(source[start:excerpt.end])
		costs[i] = len(encoded) - 2
		if slices.Contains(chapter.SourceIDs, excerpt.alias) {
			selected[i] = true
			remaining -= costs[i]
		}
		start = excerpt.end
	}
	if remaining < 0 {
		return chapterInput, aliases
	}
	visible := maps.Clone(aliases)
	var body strings.Builder
	start = 0
	for i, excerpt := range x.excerpts {
		if selected[i] {
			body.WriteString(source[start:excerpt.end])
		} else if costs[i] <= remaining {
			body.WriteString(source[start:excerpt.end])
			remaining -= costs[i]
		} else {
			delete(visible, excerpt.alias)
		}
		start = excerpt.end
	}
	if body.Len() == 0 {
		return chapterInput, aliases
	}
	chapterInput["source_text"] = body.String()
	return chapterInput, visible
}

type sourceExcerpt struct {
	alias string
	end   int
}

func canonicalReferences(ids []string, aliases map[string]string) []string {
	out := []string{}
	for _, id := range ids {
		if canonical, ok := aliases[id]; ok {
			out = append(out, canonical)
		}
	}
	return unique(out)
}
func publicChapter(c ExplainerChapter, aliases map[string]string) GuideSection {
	s := GuideSection{Role: Role(c.ID), Title: c.Title, Blocks: []GuideBlock{}, Claims: []GuideClaim{}, ClaimIDs: []string{}, Version: 1, PublicationStatus: "published"}
	for i, b := range c.Blocks {
		block := GuideBlock{ID: fmt.Sprintf("%s-b%d", c.ID, i+1), Kind: b.Kind, Text: b.Text, Attribution: AttributionTeaching, ClaimIDs: []string{}, EvidenceIDs: canonicalReferences(b.EvidenceIDs, aliases)}
		if b.Kind == "paragraph" && c.ID != "glossary" && strings.TrimSpace(b.Emphasis) != "" && strings.Count(b.Text, b.Emphasis) == 1 {
			block.Emphasis = b.Emphasis
		}
		if b.Kind == "figure" {
			block.Attribution = AttributionPaper
			block.AssetID = aliases[b.FigureID]
			block.EvidenceIDs = unique(append(block.EvidenceIDs, block.AssetID))
		}
		if b.Kind == "playground" {
			block.Playground = &GuidePlayground{HTML: b.HTML}
		}
		s.Blocks = append(s.Blocks, block)
	}
	return s
}

// Sample deterministically across the entire paper, with readable page markers
// and small aliases. No additional model extraction call is needed. The byte
// bound is conservative for both Latin and CJK tokenization.
func (x *execution) explainerInput() (map[string]any, map[string]string, error) {
	budget := x.modelInputBudget()
	// Keep typical full papers (including result tables) for large-context models.
	// Small contexts still use the same conservative per-model budget.
	sourceBudget := min(128000, budget-18000)
	if sourceBudget < 500 || len(x.snap.Evidence) == 0 {
		return nil, nil, ErrContext
	}
	aliases := map[string]string{}
	title := "Research paper"
	if len(x.snap.Sections) > 0 && x.snap.Sections[0].Title != "" {
		title = x.snap.Sections[0].Title
		if utf8.RuneCountInString(title) > 1000 {
			title = string([]rune(title)[:1000])
		}
	} else {
		title = truncateUTF8(x.snap.Evidence[0].Text, 180)
	}
	figures := []ExplainerFigure{}
	sections := map[string]string{}
	for _, part := range x.snap.Sections {
		for _, id := range part.EvidenceIDs {
			sections[id] = part.Title
		}
	}
	evidenceText := map[string]string{}
	for _, ev := range x.snap.Evidence {
		evidenceText[ev.ID] = ev.Text
	}
	for _, asset := range x.snap.Assets {
		if asset.Kind != "figure" || asset.OriginalDisplay == nil {
			continue
		}
		display := asset.OriginalDisplay
		caption := strings.TrimSpace(display.Caption)
		if caption == "" {
			caption = strings.TrimSpace(evidenceText[asset.ID])
		}
		if caption == "" {
			continue
		}
		alias := fmt.Sprintf("f%d", len(figures))
		figures = append(figures, ExplainerFigure{ID: alias, Page: display.Page + 1, Caption: caption, Section: sections[asset.ID]})
		aliases[alias] = asset.ID
	}
	input := map[string]any{"language": x.scope.Language, "paper_title": title, "source_text": "", "figures": figures}
	// Parsed pages use physical, zero-based indices and can skip blank pages.
	pageCount := 0
	for _, page := range x.snap.Pages {
		pageCount = max(pageCount, page.Index+1)
	}
	if pageCount > 0 {
		input["page_count"] = pageCount
	}
	// Preserve existing request hashes when recovering a partially written guide.
	input["source_text"], x.excerpts = packExplainerSource(x.snap.Evidence, sourceBudget, aliases, false)
	if x.inputFits(input) {
		return input, aliases, nil
	}
	input["source_text"] = ""
	// Keep room for the shared chapter plan. Cap only oversized catalogs and
	// keep complete captions so figure translations always have the source text.
	metadata, _ := json.MarshalIndent(input, "", "")
	if len(metadata) > (budget-18000)/2 {
		remaining := (budget - 18000) / 2
		kept := []ExplainerFigure{}
		for _, figure := range figures {
			raw, _ := json.MarshalIndent(figure, "", "")
			if len(raw)+2 > remaining {
				delete(aliases, figure.ID)
				continue
			}
			remaining -= len(raw) + 2
			kept = append(kept, figure)
		}
		input["figures"] = kept
	}
	// Budget the JSON-encoded excerpts after reserving room for the chapter plan.
	base, _ := json.MarshalIndent(input, "", "")
	available := budget - 6000 - len(base)
	sourceBudget = min(sourceBudget, available-min(12000, available/2))
	if sourceBudget < 500 {
		return nil, nil, ErrContext
	}
	input["source_text"], x.excerpts = packExplainerSource(x.snap.Evidence, sourceBudget, aliases, true)
	if !x.inputFits(input) {
		return nil, nil, ErrContext
	}
	return input, aliases, nil
}
func packExplainerSource(evidence []GuideEvidence, sourceBudget int, aliases map[string]string, escaped bool) (string, []sourceExcerpt) {
	size := func(s string) int {
		if !escaped {
			return len(s)
		}
		return len(mustMarshal(s)) - 2
	}
	// Preserve the complete paper whenever it fits. For long papers, select
	// whole paragraphs across the source before considering bounded excerpts of
	// unusually large blocks (for example, a parsed table). Never truncate every
	// paragraph to a fixed tiny prefix.
	entries := make([]string, len(evidence))
	total := 0
	for i, ev := range evidence {
		delete(aliases, fmt.Sprintf("s%d", i))
		entries[i] = fmt.Sprintf("[s%d page %d] %s\n", i, ev.Locator.Page+1, ev.Text)
		total += size(entries[i])
	}
	selected := map[int]string{}
	if total <= sourceBudget {
		for i, entry := range entries {
			selected[i] = entry
		}
	} else {
		// Breadth-first interval subdivision visits both ends and then the middle
		// of every remaining span, so early selections cover the whole paper.
		order := []int{0}
		if len(entries) > 1 {
			order = append(order, len(entries)-1)
		}
		spans := [][2]int{{1, len(entries) - 2}}
		for len(spans) > 0 {
			span := spans[0]
			spans = spans[1:]
			if span[0] > span[1] {
				continue
			}
			mid := (span[0] + span[1]) / 2
			order = append(order, mid)
			spans = append(spans, [2]int{span[0], mid - 1}, [2]int{mid + 1, span[1]})
		}
		remaining := sourceBudget
		blockLimit := min(sourceBudget, max(2000, sourceBudget/8))
		for _, i := range order {
			entry := entries[i]
			if size(entry) > blockLimit {
				limit := min(blockLimit-5, len(entry))
				if escaped {
					limit = sort.Search(limit+1, func(n int) bool { return size(truncateUTF8(entry, n)) > blockLimit-6 }) - 1
				}
				entry = truncateUTF8(entry, limit) + " …\n"
			}
			if size(entry) <= remaining {
				selected[i] = entry
				remaining -= size(entry)
			}
		}
	}
	indices := make([]int, 0, len(selected))
	for i := range selected {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	var text strings.Builder
	excerpts := make([]sourceExcerpt, 0, len(indices))
	for _, i := range indices {
		text.WriteString(selected[i])
		alias := fmt.Sprintf("s%d", i)
		aliases[alias] = evidence[i].ID
		excerpts = append(excerpts, sourceExcerpt{alias: alias, end: text.Len()})
	}
	return text.String(), excerpts
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}

// Validate identifiers only; this is not a scientific review or a model call.
func validateExplainerFigures(plan ExplainerPlan, chapter *ExplainerChapter, figures []ExplainerFigure) error {
	known := map[string]bool{}
	for _, f := range figures {
		known[f.ID] = true
	}
	for _, c := range plan.Chapters {
		for _, id := range c.FigureIDs {
			if !known[id] {
				return ErrEvidence
			}
		}
	}
	if chapter != nil {
		for _, b := range chapter.Blocks {
			if b.Kind == "figure" && !known[b.FigureID] {
				return ErrEvidence
			}
		}
	}
	return nil
}
