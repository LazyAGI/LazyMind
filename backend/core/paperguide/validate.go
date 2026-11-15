package paperguide

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"lazymind/core/pdfir"
)

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidGuide, field) }

// ValidateSnapshot confirms the trusted identity and digest and rebuilds the
// derived catalog, so altered locators and cross-scope IDs cannot survive load.
func ValidateSnapshot(s GuideSourceSnapshot, identity SourceIdentity, irDigest string) error {
	if s.Identity != identity || !validSHA(irDigest) || s.IRDigest != irDigest {
		return fmt.Errorf("%w: identity or digest mismatch", ErrInvalidSource)
	}
	warnings := s.Coverage.Warnings
	if s.CatalogVersion == SourceCatalogVersion || s.CatalogVersion == sourceCatalogV5 || s.CatalogVersion == "paper-guide-source-v2" || s.CatalogVersion == "paper-guide-source-v3" || s.CatalogVersion == "paper-guide-source-v4" {
		warnings = s.SourceWarnings
	}
	if len(warnings) == 0 {
		warnings = nil
	}
	d := pdfir.Document{Precision: s.Precision, Version: s.IRVersion, ProjectionVersion: s.ProjectionVersion, SourceSHA256: s.Identity.SourceSHA256,
		Parser: s.Parser, CoordinateSpace: s.CoordinateSpace, Pages: s.Pages, Blocks: s.Blocks, Warnings: warnings}
	original, err := buildSourceSnapshot(d, identity, s.CatalogVersion)
	if err != nil || !reflect.DeepEqual(s, original) {
		return fmt.Errorf("%w: catalog mismatch", ErrInvalidSource)
	}
	return nil
}

// ValidateEvidence accepts only the server-built canonical entry. Models cite
// its ID and never supply locator coordinates, source hashes or source paths.
func ValidateEvidence(s GuideSourceSnapshot, e GuideEvidence) error {
	for _, item := range s.Evidence {
		if item.ID == e.ID {
			if reflect.DeepEqual(item, e) {
				return nil
			}
			break
		}
	}
	return invalid("evidence ID or locator")
}

var itemID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var unsafeMarkup = regexp.MustCompile("(?i)</?[a-z][a-z0-9:-]*(?:[\\s/][^<>]*)?>|<\\s*[!?]|(?:javascript|vbscript|data|file):|!?\\[[^\\]\\n]*\\]\\s*(?:\\(|\\[)|(?m:^\\s*\\[[^\\]\\n]+\\]:)|!\\[\\[|(?:https?://|www\\.)|" + "\x60{3}|(?m:^\\s*~{3})")

var inlineMath = regexp.MustCompile(`\$[^$\n]+\$`)
var mathAngleVariable = regexp.MustCompile(`(?i)<[c-hj-ortv-z]>`)

// Recognize compact angle notation for single-letter variables that are not
// HTML tag names. Keep all other markup checks active inside math.
func unsafeProse(s string) bool {
	checked := inlineMath.ReplaceAllStringFunc(s, func(math string) string {
		return mathAngleVariable.ReplaceAllString(math, "x")
	})
	return unsafeMarkup.MatchString(checked)
}

func prose(s string, limit int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit &&
		!strings.ContainsAny(s, "\x00\u202a\u202b\u202d\u202e\u2066\u2067\u2068\u2069") && !unsafeProse(s)
}
func texts(xs []string, count, limit int) bool {
	if len(xs) > count {
		return false
	}
	for _, x := range xs {
		if !prose(x, limit) {
			return false
		}
	}
	return true
}
func argument(a ArgumentType) bool {
	switch a {
	case ArgumentMethod, ArgumentExperiment, ArgumentObservational, ArgumentQualitative, ArgumentTheory, ArgumentHumanities, ArgumentSynthesis, ArgumentResource:
		return true
	}
	return false
}
func attribution(a Attribution) bool {
	switch a {
	case AttributionPaper, AttributionTeaching, AttributionInterpretation, AttributionExternal:
		return true
	}
	return false
}
func addID(m map[string]bool, id string) bool {
	if !itemID.MatchString(id) || m[id] {
		return false
	}
	m[id] = true
	return true
}
func refs(ids []string, allowed map[string]bool, max int) bool {
	if len(ids) > max {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !allowed[id] || !addID(seen, id) {
			return false
		}
	}
	return true
}

// RestrictEvidence returns a validation view for the exact model packet. Plan
// membership and other business checks still use the immutable full snapshot.
func RestrictEvidence(s GuideSourceSnapshot, ids []string) (GuideSourceSnapshot, error) {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !addID(wanted, id) {
			return GuideSourceSnapshot{}, invalid("duplicate packet ID")
		}
	}
	known := map[string]GuideEvidence{}
	for _, e := range s.Evidence {
		known[e.ID] = e
	}
	for id := range wanted {
		if _, ok := known[id]; !ok {
			return GuideSourceSnapshot{}, invalid("unknown packet ID")
		}
	}
	// A table is an indivisible source group for admission and extraction.
	// Closure retains every physical row and ensures all model parent references
	// are present even when a claim initially selected just one fragment.
	for changed := true; changed; {
		changed = false
		for id := range wanted {
			e := known[id]
			refs := append([]string{}, e.FragmentEvidenceIDs...)
			if e.ParentAssetID != "" {
				refs = append(refs, e.ParentAssetID)
			}
			for _, ref := range refs {
				if _, ok := known[ref]; !ok {
					return GuideSourceSnapshot{}, invalid("unknown table reference")
				}
				if !wanted[ref] {
					wanted[ref] = true
					changed = true
				}
			}
		}
	}
	selected := []GuideEvidence{}
	for _, e := range s.Evidence {
		if wanted[e.ID] {
			selected = append(selected, e)
		}
	}
	if len(selected) != len(wanted) {
		return GuideSourceSnapshot{}, invalid("unknown packet ID")
	}
	s.Evidence = selected
	return s, nil
}
func evidenceIndex(s GuideSourceSnapshot) (map[string]bool, map[string]string) {
	ev := map[string]bool{}
	assets := map[string]string{}
	for _, e := range s.Evidence {
		ev[e.ID] = true
	}
	for _, a := range s.Assets {
		if ev[a.ID] {
			assets[a.ID] = a.Kind
		}
	}
	return ev, assets
}
func assetIDs(kinds map[string]string) map[string]bool {
	out := map[string]bool{}
	for id := range kinds {
		out[id] = true
	}
	return out
}

func validateClaims(cs []GuideClaim, ev map[string]bool, ids map[string]bool, max int) error {
	if len(cs) > max {
		return invalid("claim count")
	}
	for _, c := range cs {
		if !addID(ids, c.ID) || !prose(c.Statement, 1200) || !attribution(c.Attribution) ||
			(c.ArgumentType != nil && !argument(*c.ArgumentType)) || !refs(c.EvidenceIDs, ev, 32) || !texts(c.Conditions, 16, 400) {
			return invalid("claim fields or evidence")
		}
		switch c.SupportStatus {
		case SupportSupported, SupportPartial:
			if len(c.EvidenceIDs) == 0 || c.Attribution == AttributionTeaching || c.Attribution == AttributionExternal {
				return invalid("unbound support claim")
			}
		case SupportUnsupported, SupportUnavailable, SupportNotApplicable:
		default:
			return invalid("support status")
		}
		if c.Attribution == AttributionPaper && c.SupportStatus == SupportNotApplicable {
			return invalid("paper claim without support status")
		}
		if c.Attribution == AttributionPaper && len(c.EvidenceIDs) == 0 && c.SupportStatus != SupportUnavailable {
			return invalid("paper claim without evidence")
		}
	}
	return nil
}
func ValidateExtract(s GuideSourceSnapshot, out GuideExtract) error {
	ev, _ := evidenceIndex(s)
	if !texts(out.OpenQuestions, 24, 500) {
		return invalid("open questions")
	}
	return validateClaims(out.Claims, ev, map[string]bool{}, 48)
}

func ValidatePlan(s GuideSourceSnapshot, out GuidePlan) error {
	ev, kinds := evidenceIndex(s)
	assets := assetIDs(kinds)
	if out.Profiles.Primary == nil && (!prose(out.Profiles.UncertaintyReason, 600) || len(out.Profiles.Secondary) != 0) ||
		out.Profiles.UncertaintyReason != "" && !prose(out.Profiles.UncertaintyReason, 600) ||
		!texts(out.Questions, 16, 600) || len(out.Questions) == 0 || !texts(out.TermPolicy, 32, 600) {
		return invalid("profiles, questions or terms")
	}
	profiles := map[ArgumentType]bool{}
	if a := out.Profiles.Primary; a != nil {
		if !argument(*a) {
			return invalid("primary profile")
		}
		profiles[*a] = true
	}
	for _, a := range out.Profiles.Secondary {
		if !argument(a) || profiles[a] {
			return invalid("secondary profile")
		}
		profiles[a] = true
	}
	claims := map[string]bool{}
	if err := validateClaims(out.Claims, ev, claims, 96); err != nil {
		return err
	}
	if len(out.Sections) != len(Roles()) || len(out.RequiredAssets) > 128 {
		return invalid("section or asset count")
	}
	decisions := map[string]bool{}
	included := map[string]bool{}
	for _, a := range out.RequiredAssets {
		if !assets[a.ID] || !addID(decisions, a.ID) || !prose(a.Reason, 500) {
			return invalid("asset decision")
		}
		if a.Included {
			included[a.ID] = true
		}
	}
	usedClaims, usedAssets := map[string]bool{}, map[string]bool{}
	for i, r := range Roles() {
		p := out.Sections[i]
		if p.Role != r || !prose(p.Title, 180) || !prose(p.Focus, 1000) || !texts(p.Terms, 32, 220) ||
			!refs(p.ClaimIDs, claims, 96) || !refs(p.EvidenceIDs, ev, 128) || !refs(p.AssetIDs, included, 32) {
			return invalid("section plan")
		}
		for _, id := range p.ClaimIDs {
			usedClaims[id] = true
		}
		for _, id := range p.AssetIDs {
			usedAssets[id] = true
		}
	}
	if len(usedClaims) != len(claims) || len(usedAssets) != len(included) {
		return invalid("plan coverage")
	}
	return nil
}

func ValidateSection(s GuideSourceSnapshot, p GuidePlan, role Role, out GuideSection) error {
	ev, kinds := evidenceIndex(s)
	var expected *GuideSectionPlan
	for i := range p.Sections {
		if p.Sections[i].Role == role {
			expected = &p.Sections[i]
			break
		}
	}
	if expected == nil || out.Role != role || out.Title != expected.Title || !prose(out.Title, 180) ||
		!prose(out.Summary, 1200) || len(out.Blocks) == 0 || len(out.Blocks) > 64 {
		return invalid("section role, title or blocks")
	}
	claims := map[string]bool{}
	for _, c := range p.Claims {
		claims[c.ID] = true
	}
	if err := validateClaims(out.Claims, ev, claims, 48); err != nil {
		return err
	}
	if !refs(out.ClaimIDs, claims, 96) {
		return invalid("section claim IDs")
	}
	sectionClaims := map[string]bool{}
	for _, id := range out.ClaimIDs {
		sectionClaims[id] = true
	}
	allowedAssets := map[string]bool{}
	for _, id := range expected.AssetIDs {
		allowedAssets[id] = true
	}
	seen, used := map[string]bool{}, map[string]bool{}
	for _, b := range out.Blocks {
		if !addID(seen, b.ID) || !attribution(b.Attribution) || !prose(b.Text, 7500) ||
			!refs(b.ClaimIDs, sectionClaims, 48) || !refs(b.EvidenceIDs, ev, 32) {
			return invalid("block fields or references")
		}
		if b.Attribution == AttributionPaper && len(b.ClaimIDs) == 0 && len(b.EvidenceIDs) == 0 {
			return invalid("unbound paper block")
		}
		for _, id := range b.ClaimIDs {
			used[id] = true
		}
		switch b.Kind {
		case "paragraph", "heading", "list", "example", "callout", "detail":
			if b.AssetID != "" {
				return invalid("asset on text block")
			}
		case "figure", "formula", "table":
			if kinds[b.AssetID] != b.Kind || !allowedAssets[b.AssetID] {
				return invalid("wrong or unplanned asset")
			}
		default:
			return invalid("block kind")
		}
	}
	if len(used) != len(sectionClaims) {
		return invalid("orphan section claims")
	}
	for _, id := range expected.ClaimIDs {
		if !used[id] {
			return invalid("missing planned claim")
		}
	}
	for _, c := range p.Claims {
		if used[c.ID] && !refs(c.EvidenceIDs, ev, 32) {
			return invalid("planned claim evidence outside packet")
		}
	}
	for _, c := range out.Claims {
		if !used[c.ID] {
			return invalid("orphan new claim")
		}
	}
	return nil
}

func ValidateReview(p GuidePlan, sections []GuideSection, out GuideReview) error {
	claims, blocks := map[string]bool{}, map[string]bool{}
	for _, c := range p.Claims {
		if !addID(claims, c.ID) {
			return invalid("ambiguous review claim ID")
		}
	}
	for _, s := range sections {
		for _, c := range s.Claims {
			if !addID(claims, c.ID) {
				return invalid("ambiguous review claim ID")
			}
		}
		for _, b := range s.Blocks {
			if !addID(blocks, b.ID) {
				return invalid("ambiguous review block ID")
			}
		}
	}
	if len(out.Findings) > 48 {
		return invalid("review findings count")
	}
	for _, f := range out.Findings {
		if !prose(f.Suggestion, 800) {
			return invalid("review suggestion")
		}
		switch f.TargetType {
		case "claim":
			if !claims[f.TargetID] {
				return invalid("review claim ID")
			}
		case "block":
			if !blocks[f.TargetID] {
				return invalid("review block ID")
			}
		default:
			return invalid("review target kind")
		}
		switch f.IssueType {
		case "unsupported", "overstatement", "missing_condition", "attribution", "causal_overreach", "visual_inference", "coverage", "coherence", "missing_explanation", "inconsistency":
		default:
			return invalid("review issue kind")
		}
	}
	return nil
}
