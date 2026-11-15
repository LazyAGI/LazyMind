package paperguide

import "sort"

const maxOriginalFigures, maxOriginalFormulas = 4, 6

type originalAssetCandidate struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
}
type originalAssetCandidates struct {
	Figure  []originalAssetCandidate `json:"figure"`
	Formula []originalAssetCandidate `json:"formula"`
}

func originalDisplayMode(o *GuideOriginalDisplay) string {
	if validBBox(o.BBox) && !(o.BBox[0] == 0 && o.BBox[1] == 0 && o.BBox[2] == 1 && o.BBox[3] == 1) {
		return "region"
	}
	return "page"
}

// Rank using only frozen source facts: recognized body vs unclassified pages,
// retained EXTRACT citations, existing original associations, then reading
// order. Distinct sections and pages get a turn before a page gets another
// candidate. Reference/appendix sections do not create a main-text requirement.
func selectOriginalAssets(s GuideSourceSnapshot, retained map[string]bool) originalAssetCandidates {
	type ranked struct {
		originalAssetCandidate
		section, page, order           int
		association, context, identity int
	}
	ranks := map[string][]ranked{}
	order := map[string]int{}
	blockEvidence := map[string][]string{}
	inPacket := map[string]GuideEvidence{}
	for i, ev := range s.Evidence {
		order[ev.ID] = i
		inPacket[ev.ID] = ev
		blockEvidence[ev.SourceBlockID] = append(blockEvidence[ev.SourceBlockID], ev.ID)
	}
	section := map[string]int{}
	keptSection, keptPage := map[int]bool{}, map[int]bool{}
	for i, sec := range s.Sections {
		for _, id := range sec.EvidenceIDs {
			section[id] = i
			if ev, ok := inPacket[id]; ok && retained[id] && ev.Text != "" {
				keptSection[i], keptPage[ev.Locator.Page] = true, true
			}
		}
	}
	for _, a := range s.Assets {
		ev, exists := inPacket[a.ID]
		o := a.OriginalDisplay
		if !exists || o == nil || o.Kind != a.Kind || o.Page != ev.Locator.Page {
			continue
		}
		index := -1
		if i, found := section[a.ID]; found {
			index = i
			switch s.Sections[i].Kind {
			case "references", "appendix":
				continue
			}
		}
		context, association := 0, 0
		if index >= 0 && s.Sections[index].Kind != "page_group" {
			association = 1
		}
		if keptSection[index] {
			context++
		}
		if keptPage[o.Page] {
			context++
		}
		if retained[a.ID] {
			association = 3
		}
		signals := 0
		if o.Caption != "" || o.Number != "" {
			signals = 1
		}
		for _, related := range a.RelatedBlocks {
			if related.Relation != "mention" && related.Relation != "caption" {
				continue
			}
			for _, id := range blockEvidence[related.BlockID] {
				if retained[id] {
					association = max(association, 2)
				}
			}
		}
		ranks[a.Kind] = append(ranks[a.Kind], ranked{originalAssetCandidate{a.ID, originalDisplayMode(o)}, index, o.Page, order[a.ID], association, context, signals})
	}
	pick := func(items []ranked, limit int) []originalAssetCandidate {
		out := make([]originalAssetCandidate, 0, limit)
		hasRegion, hasPage := false, false
		for _, item := range items {
			hasRegion = hasRegion || item.Mode == "region"
			hasPage = hasPage || item.Mode == "page"
		}
		// Retain a page-level option, alongside the independently crop-able
		// originals, when a real asset has only source-page proof.
		regionSlots := limit
		if hasPage {
			regionSlots--
		}
		usedPages, usedSections := map[int]int{}, map[int]int{}
		for len(out) < limit && len(items) != 0 {
			mode := "region"
			if !hasRegion || len(out) == regionSlots {
				mode = "page"
			}
			sort.SliceStable(items, func(i, j int) bool {
				a, b := items[i], items[j]
				if (a.Mode == mode) != (b.Mode == mode) {
					return a.Mode == mode
				}
				if a.association != b.association {
					return a.association > b.association
				}
				if usedPages[a.page] != usedPages[b.page] {
					return usedPages[a.page] < usedPages[b.page]
				}
				if usedSections[a.section] != usedSections[b.section] {
					return usedSections[a.section] < usedSections[b.section]
				}
				if a.context != b.context {
					return a.context > b.context
				}
				if a.identity != b.identity {
					return a.identity > b.identity
				}
				if a.page != b.page {
					return a.page < b.page
				}
				return a.order < b.order
			})
			item := items[0]
			if item.Mode == "page" && containsCandidateMode(out, "page") {
				break
			}
			out = append(out, item.originalAssetCandidate)
			usedPages[item.page]++
			usedSections[item.section]++
			items = items[1:]
		}
		return out
	}
	return originalAssetCandidates{Figure: pick(ranks["figure"], maxOriginalFigures), Formula: pick(ranks["formula"], maxOriginalFormulas)}
}

func containsCandidateMode(items []originalAssetCandidate, mode string) bool {
	for _, item := range items {
		if item.Mode == mode {
			return true
		}
	}
	return false
}

func (c originalAssetCandidates) empty() bool { return len(c.Figure) == 0 && len(c.Formula) == 0 }

// Do not drop the last region candidate of a known kind, or the only page
// witness of a page-only kind. Optional tails may go to admit a long packet.
func (c *originalAssetCandidates) trim() bool {
	trim := func(list *[]originalAssetCandidate) bool {
		if n := len(*list); n > 1 {
			*list = (*list)[:n-1]
			return true
		}
		return false
	}
	if len(c.Formula) >= len(c.Figure) {
		return trim(&c.Formula)
	}
	return trim(&c.Figure)
}

func validateOriginalPlan(s GuideSourceSnapshot, out GuidePlan, kinds map[string]string, included map[string]bool) error {
	if s.originalCandidates == nil {
		return nil // historical/unit read paths do not carry a new Plan packet
	}
	pages := map[string]int{}
	for _, evidence := range s.Evidence {
		pages[evidence.ID] = evidence.Locator.Page
	}
	displays := map[string]*GuideOriginalDisplay{}
	for _, asset := range s.Assets {
		displays[asset.ID] = asset.OriginalDisplay
	}
	candidates := map[string]bool{}
	for _, group := range []struct {
		kind  string
		limit int
		list  []originalAssetCandidate
	}{{"figure", maxOriginalFigures, s.originalCandidates.Figure}, {"formula", maxOriginalFormulas, s.originalCandidates.Formula}} {
		if len(group.list) > group.limit {
			return invalid("original asset candidate count")
		}
		hasRegion, chosenRegion := false, false
		for _, c := range group.list {
			o := displays[c.ID]
			if kinds[c.ID] != group.kind || o == nil || o.Kind != group.kind || o.Page != pages[c.ID] || c.Mode != originalDisplayMode(o) || !addID(candidates, c.ID) {
				return invalid("original asset candidate provenance")
			}
			if c.Mode == "region" {
				hasRegion = true
				chosenRegion = chosenRegion || included[c.ID]
			}
		}
		if hasRegion && !chosenRegion {
			return invalid("planned original region missing: " + group.kind)
		}
	}
	for _, decision := range out.RequiredAssets {
		if kinds[decision.ID] != "table" && !candidates[decision.ID] {
			return invalid("original asset outside plan candidates")
		}
	}
	return nil
}
