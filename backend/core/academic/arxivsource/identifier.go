// Package arxivsource parses arXiv identifiers and retrieves verified metadata.
// It never fetches the URL supplied by a user: inputs resolve to identifiers,
// which are looked up through the official API.
package arxivsource

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Identifier keeps the work identity separate from a requested revision.
// An empty Version means that Lookup must resolve the latest published version.
type Identifier struct {
	BaseID  string `json:"base_id"`
	Version string `json:"version,omitempty"`
}

func (id Identifier) ID() string     { return id.BaseID + id.Version }
func (id Identifier) AbsURL() string { return "https://arxiv.org/abs/" + id.ID() }
func (id Identifier) PDFURL() string { return "https://arxiv.org/pdf/" + id.ID() }

var (
	modernID        = regexp.MustCompile(`^(\d{2})(\d{2})\.(\d{4,5})([vV][1-9]\d*)?$`)
	legacyID        = regexp.MustCompile(`^([a-zA-Z][a-zA-Z-]*)(?:\.([a-zA-Z]{2}))?/(\d{2})(\d{2})(\d{3})([vV][1-9]\d*)?$`)
	arxivPrefix     = regexp.MustCompile(`(?i)^arxiv[\s\p{Zs}]*:[\s\p{Zs}]*`)
	doiPrefix       = regexp.MustCompile(`(?i)^doi\s*:\s*`)
	markdownLink    = regexp.MustCompile(`^\[[^\]\r\n]*\]\(\s*(<[^>\r\n]+>|[^\s()]+)(?:\s+"[^"\r\n]*")?\s*\)$`)
	categorySuffix  = regexp.MustCompile(`^([^\[\]]+)\[([A-Za-z][A-Za-z.-]*)\]$`)
	knownCategories = categorySet()
)

// Only remove actual arXiv category labels, never arbitrary bracketed text.
// Source: https://arxiv.org/category_taxonomy (2026-10-21).
func categorySet() map[string]bool {
	const categories = `
cs.AI cs.AR cs.CC cs.CE cs.CG cs.CL cs.CR cs.CV cs.CY cs.DB cs.DC cs.DL cs.DM cs.DS cs.ET cs.FL cs.GL cs.GR cs.GT cs.HC cs.IR cs.IT cs.LG cs.LO cs.MA cs.MM cs.MS cs.NA cs.NE cs.NI cs.OH cs.OS cs.PF cs.PL cs.RO cs.SC cs.SD cs.SE cs.SI cs.SY
econ.EM econ.GN econ.TH eess.AS eess.IV eess.SP eess.SY
math.AC math.AG math.AP math.AT math.CA math.CO math.CT math.CV math.DG math.DS math.FA math.GM math.GN math.GR math.GT math.HO math.IT math.KT math.LO math.MG math.MP math.NA math.NT math.OA math.OC math.PR math.QA math.RA math.RT math.SG math.SP math.ST
astro-ph.CO astro-ph.EP astro-ph.GA astro-ph.HE astro-ph.IM astro-ph.SR
cond-mat.dis-nn cond-mat.mes-hall cond-mat.mtrl-sci cond-mat.other cond-mat.quant-gas cond-mat.soft cond-mat.stat-mech cond-mat.str-el cond-mat.supr-con
gr-qc hep-ex hep-lat hep-ph hep-th math-ph nucl-ex nucl-th quant-ph
nlin.AO nlin.CD nlin.CG nlin.PS nlin.SI
physics.acc-ph physics.ao-ph physics.app-ph physics.atm-clus physics.atom-ph physics.bio-ph physics.chem-ph physics.class-ph physics.comp-ph physics.data-an physics.ed-ph physics.flu-dyn physics.gen-ph physics.geo-ph physics.hist-ph physics.ins-det physics.med-ph physics.optics physics.plasm-ph physics.pop-ph physics.soc-ph physics.space-ph
q-bio.BM q-bio.CB q-bio.GN q-bio.MN q-bio.NC q-bio.OT q-bio.PE q-bio.QM q-bio.SC q-bio.TO
q-fin.CP q-fin.EC q-fin.GN q-fin.MF q-fin.PM q-fin.PR q-fin.RM q-fin.ST q-fin.TR
stat.AP stat.CO stat.ME stat.ML stat.OT stat.TH`
	result := make(map[string]bool)
	for _, category := range strings.Fields(categories) {
		result[category] = true
	}
	return result
}

// Parse accepts common copied arXiv identifiers, official paper URLs, and arXiv
// DOIs. It consumes the entire input rather than extracting a plausible ID from
// an arbitrary URL or prose. Identifier rules and legacy canonicalization:
// https://info.arxiv.org/help/arxiv_identifier_for_services.html
func Parse(input string) (Identifier, error) {
	value := strings.TrimSpace(input)
	if value == "" || len(value) > 4096 {
		return Identifier{}, invalidInput()
	}
	if match := markdownLink.FindStringSubmatch(value); match != nil {
		value = match[1]
	}
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		value = value[1 : len(value)-1]
	}
	value = arxivPrefix.ReplaceAllString(value, "")
	value = doiPrefix.ReplaceAllString(value, "")
	if match := categorySuffix.FindStringSubmatch(value); match != nil {
		identifier := strings.TrimSpace(match[1])
		if !knownCategories[match[2]] || (!modernID.MatchString(identifier) && !legacyID.MatchString(identifier)) {
			return Identifier{}, invalidInput()
		}
		value = identifier
	}
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	lower := strings.ToLower(value)
	for _, host := range []string{"arxiv.org/", "www.arxiv.org/", "export.arxiv.org/", "doi.org/", "dx.doi.org/"} {
		if strings.HasPrefix(lower, host) {
			value = "https://" + value
			break
		}
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return Identifier{}, invalidInput()
		}
		if port := parsed.Port(); port != "" && !((parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80")) {
			return Identifier{}, invalidInput()
		}
		switch strings.ToLower(parsed.Hostname()) {
		case "arxiv.org", "www.arxiv.org", "export.arxiv.org":
			parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 2)
			if len(parts) != 2 {
				return Identifier{}, invalidInput()
			}
			switch parts[0] {
			case "abs", "pdf", "html", "format", "src", "e-print":
			default:
				return Identifier{}, invalidInput()
			}
			value = strings.TrimSuffix(parts[1], "/")
		case "doi.org", "dx.doi.org":
			value = strings.TrimPrefix(parsed.Path, "/")
			if !strings.HasPrefix(strings.ToLower(value), "10.48550/arxiv.") {
				return Identifier{}, invalidInput()
			}
		default:
			return Identifier{}, invalidInput()
		}
	}
	if strings.HasPrefix(strings.ToLower(value), "10.48550/arxiv.") {
		value = value[len("10.48550/arxiv."):]
	}
	if strings.HasSuffix(strings.ToLower(value), ".pdf") {
		value = value[:len(value)-4]
	}
	if m := modernID.FindStringSubmatch(value); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		sequence, _ := strconv.Atoi(m[3])
		if month < 1 || month > 12 || sequence < 1 {
			return Identifier{}, invalidInput()
		}
		// 0704–1412 used four digits; 1501 onward uses five. 00–06 may
		// represent 2100–2106 under the published identifier scheme.
		fourDigits := year >= 7 && year <= 14
		if (year == 7 && month < 4) || (fourDigits && len(m[3]) != 4) || (!fourDigits && len(m[3]) != 5) {
			return Identifier{}, invalidInput()
		}
		return Identifier{BaseID: m[1] + m[2] + "." + m[3], Version: strings.ToLower(m[4])}, nil
	}
	if m := legacyID.FindStringSubmatch(value); m != nil {
		year, _ := strconv.Atoi(m[3])
		month, _ := strconv.Atoi(m[4])
		sequence, _ := strconv.Atoi(m[5])
		if month < 1 || month > 12 || sequence < 1 || (year > 7 && year < 91) || (year == 91 && month < 7) || (year == 7 && month > 3) {
			return Identifier{}, invalidInput()
		}
		archive := strings.ToLower(m[1])
		if m[2] != "" && archive != "math" && archive != "cs" && archive != "nlin" && archive != "q-bio" {
			return Identifier{}, invalidInput()
		}
		// The old subject class is redundant. arXiv's preferred external
		// form is math/0611800, including when copied as math.CA/0611800.
		return Identifier{BaseID: archive + "/" + m[3] + m[4] + m[5], Version: strings.ToLower(m[6])}, nil
	}
	return Identifier{}, invalidInput()
}

func invalidInput() error {
	return &LookupError{Code: "invalid_input", Message: "无效的 arXiv 编号或链接；请使用完整编号、arXiv 官方论文链接或 10.48550/arXiv DOI"}
}
