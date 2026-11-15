package paperguide

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// The shared Python tests and testdata/request-budget.json measure each real
// instruction, output schema and input wrapper. The largest fixed prefix is
// 9600 UTF-8 bytes including the maximum SECTION map. The 10240 reserve also
// covers the 87-byte default system prompt and 512 bytes of message framing.
// PLAN map entries add at most 18 bytes per listed alias. The model packet
// saves at least 62 bytes for that evidence ID (67 canonical bytes vs. at most
// 5 alias bytes at 2048 evidence entries). The repeat credit leaves that first
// occurrence charged for each ID. The fixed reserve covers the map
// label and empty object. One UTF-8 byte is conservatively one token.
const guidePromptReserve = 10240

// Python's validated model_dump adds bounded default fields (including empty
// top-level arrays, locator start/cells/headers) and normalizes bbox floats.
// MarshalIndent below covers its JSON separator whitespace. These extra bounds
// cover the remaining expansion and are guarded for every input schema at its
// 2048-evidence limit, rather than assuming Go's compact JSON is the full prompt.
const guideInputDefaultsReserve = 256
const guideEvidenceExpansionReserve = 64

const (
	guideMaxEvidence       = 2048
	guideMaxRawInputBound  = 1 << 20
	canonicalSourceIDBytes = 67
)

var canonicalSourceID = regexp.MustCompile(`^ev_[a-f0-9]{64}$`)

type sourceReferencePath struct {
	path          string
	optionalField string
	nullable      bool
}

// Match Python SourceReferences.INPUT_PATHS per task. The order, especially
// evidence.*.id first, is checked against the model schema in both languages.
var commonSourceReferencePaths = []sourceReferencePath{
	{path: "evidence.*.id"},
	{path: "evidence.*.parent_asset_id", optionalField: "parent_asset_id", nullable: true},
	{path: "evidence.*.fragment_evidence_ids.*", optionalField: "fragment_evidence_ids", nullable: true},
	{path: "source_sections.*.evidence_ids.*", optionalField: "source_sections"},
	{path: "original_asset_candidates.figure.*.id", optionalField: "original_asset_candidates", nullable: true},
	{path: "original_asset_candidates.formula.*.id", optionalField: "original_asset_candidates", nullable: true},
}

var planSourceReferencePaths = []sourceReferencePath{
	{path: "extracts.*.claims.*.evidence_ids.*", optionalField: "extracts"},
}

var sectionSourceReferencePaths = []sourceReferencePath{
	{path: "plan.claims.*.evidence_ids.*"},
	{path: "plan.sections.*.evidence_ids.*"},
	{path: "plan.sections.*.asset_ids.*"},
	{path: "plan.required_assets.*.id"},
}

var reviewSourceReferencePaths = []sourceReferencePath{
	{path: "sections.*.claims.*.evidence_ids.*"},
	{path: "sections.*.blocks.*.evidence_ids.*"},
	{path: "sections.*.blocks.*.asset_id", optionalField: "asset_id", nullable: true},
}

func sourceReferencePaths(task string) [][]sourceReferencePath {
	switch task {
	case "paper.guide.extract":
		return [][]sourceReferencePath{commonSourceReferencePaths}
	case "paper.guide.plan":
		return [][]sourceReferencePath{commonSourceReferencePaths, planSourceReferencePaths}
	case "paper.guide.section":
		return [][]sourceReferencePath{commonSourceReferencePaths, sectionSourceReferencePaths}
	case "paper.guide.review":
		return [][]sourceReferencePath{commonSourceReferencePaths, sectionSourceReferencePaths, reviewSourceReferencePaths}
	default:
		return nil
	}
}

// guideInputBound applies transport caps before any alias savings. The
// evidence count comes from the same JSON for structs and generic inputs.
func guideInputBound(task string, raw []byte) (int, bool) {
	bound := len(raw) + guideInputDefaultsReserve
	if bound > guideMaxRawInputBound {
		return 0, false
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil || input == nil {
		return 0, false
	}
	if evidence, present := input["evidence"]; present {
		entries, ok := evidence.([]any)
		if !ok || len(entries) > guideMaxEvidence {
			return 0, false
		}
		bound += len(entries) * guideEvidenceExpansionReserve
	}
	if bound > guideMaxRawInputBound {
		return 0, false
	}
	return bound + guidePromptReserve - sourceAliasRepeatCredit(task, input), true
}

type sourceAliasOccurrences struct {
	total  int
	unique map[string]struct{}
}

func sourceAliasRepeatCredit(task string, input map[string]any) int {
	occurrences := sourceAliasOccurrences{unique: make(map[string]struct{})}
	for _, group := range sourceReferencePaths(task) {
		for _, path := range group {
			if !occurrences.collect(input, path, path.path) {
				return 0 // Never credit a partially traversed malformed input.
			}
		}
	}
	// Count raw paths before Python's deduplication and SECTION projection.
	// Even context-only references influence the largest possible alias.
	aliasBytes := 1 + len(strconv.Itoa(occurrences.total))
	return (occurrences.total - len(occurrences.unique)) * max(0, canonicalSourceIDBytes-aliasBytes)
}

func (o *sourceAliasOccurrences) collect(value any, path sourceReferencePath, remaining string) bool {
	if remaining == "" {
		id, ok := value.(string)
		if !ok || !canonicalSourceID.MatchString(id) {
			return false
		}
		o.total++
		o.unique[id] = struct{}{}
		return true
	}
	field, tail, _ := strings.Cut(remaining, ".")
	if field == "*" {
		entries, ok := value.([]any)
		if !ok {
			return false
		}
		for _, entry := range entries {
			if !o.collect(entry, path, tail) {
				return false
			}
		}
		return true
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	child, present := object[field]
	if field == path.optionalField && (!present || (child == nil && path.nullable)) {
		return true
	}
	return present && o.collect(child, path, tail)
}
