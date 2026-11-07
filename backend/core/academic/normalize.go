package academic

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	doiPattern            = regexp.MustCompile(`(?i)10\.\d{4,9}/[-._;()/:A-Z0-9]+`)
	doiPlaceholderPattern = regexp.MustCompile(`(?i)^10\.\d{4,9}/s?x{3,}-y{3,}-z{3,}(?:-\d+)?$`)
	arxivPattern          = regexp.MustCompile(`(?i)(?:arxiv\s*:\s*)?((?:\d{4}\.\d{4,5}|[a-z][a-z.-]*/\d{7})(?:v(\d+))?)`)
	arxivExplicitPattern  = regexp.MustCompile(`(?i)(?:arxiv\s*[:.]\s*|arxiv\s*\.\s*org\s*/\s*(?:abs|pdf)\s*/\s*)((?:\d{4}\.\s*\d{4,5}|[a-z-]+/\s*\d{7})(?:v\s*(\d+))?)`)
	yearPattern           = regexp.MustCompile(`\b((?:19|20)\d{2})[a-z]?\b`)
)

func NormalizeDOI(value string) string {
	v := strings.TrimSpace(strings.ToLower(value))
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "http://dx.doi.org/", "doi:"} {
		v = strings.TrimPrefix(v, prefix)
	}
	if match := doiPattern.FindString(v); match != "" {
		v = strings.ToLower(match)
	}
	v = strings.TrimRight(v, ".,;)]}")
	return regexp.MustCompile(`(/arxiv\.\d{4}\.\d{4,5})v\d+$`).ReplaceAllString(v, "$1")
}

func NormalizeArxivID(value string) (base, version string) {
	v := strings.TrimSpace(value)
	v = strings.ReplaceAll(v, " ", "")
	v = strings.TrimPrefix(v, "https://arxiv.org/abs/")
	v = strings.TrimPrefix(v, "http://arxiv.org/abs/")
	v = strings.TrimPrefix(v, "https://arxiv.org/pdf/")
	v = strings.TrimSuffix(v, ".pdf")
	m := arxivPattern.FindStringSubmatch(v)
	if len(m) == 0 {
		return "", ""
	}
	full := strings.ToLower(m[1])
	base = regexp.MustCompile(`v\d+$`).ReplaceAllString(full, "")
	if len(m) > 2 && m[2] != "" {
		version = "v" + m[2]
	}
	return base, version
}

func NormalizeTitle(value string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func normalizeAuthor(value string) string { return NormalizeTitle(value) }

func extractStrongIDs(raw string) (doi, arxiv, version string) {
	raw = repairWrappedArxivIdentifiers(raw)
	for _, match := range referenceDOIs(raw) {
		candidate := NormalizeDOI(match)
		// Publisher templates can retain an unassigned DOI in a preprint header.
		if !doiPlaceholderPattern.MatchString(candidate) {
			doi = candidate
			break
		}
	}
	if strings.HasPrefix(doi, "10.48550/arxiv.") {
		arxiv, version = NormalizeArxivID(strings.TrimPrefix(doi, "10.48550/arxiv."))
	}
	if match := arxivExplicitPattern.FindStringSubmatch(raw); len(match) > 1 {
		arxiv, version = NormalizeArxivID(match[1])
	}
	return
}

func identifierFreeCitation(raw string) string {
	raw = arxivExplicitPattern.ReplaceAllString(raw, " ")
	raw = doiPattern.ReplaceAllString(raw, " ")
	return regexp.MustCompile(`(?i)https?://\S+`).ReplaceAllString(raw, " ")
}

func extractYear(raw string) int {
	match := yearPattern.FindStringSubmatch(identifierFreeCitation(raw))
	if len(match) < 2 {
		return 0
	}
	year, _ := strconv.Atoi(match[1])
	return year
}

func extractReferenceURL(raw string) string {
	lower := strings.ToLower(raw)
	index := strings.LastIndex(lower, "url ")
	if index < 0 {
		index = strings.Index(lower, "https")
	} else {
		index += len("url ")
	}
	if index < 0 || index >= len(raw) {
		return ""
	}
	value := strings.TrimSpace(raw[index:])
	for _, marker := range []string{" Accessed:", " accessed:", " ISBN ", " isbn "} {
		if end := strings.Index(value, marker); end >= 0 {
			value = value[:end]
		}
	}
	value = strings.TrimRight(strings.TrimSpace(value), `.,;)]}`)
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "https//", "https://")
	value = strings.ReplaceAll(value, "http//", "http://")
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return value
	}
	return ""
}

func isGitHubReferenceURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (strings.EqualFold(u.Hostname(), "github.com") || strings.EqualFold(u.Hostname(), "www.github.com"))
}

// Multiple mentions of one identity (including its arXiv DOI) are normal;
// distinct strong identities in one entry must never be silently combined.
func conflictingReferenceIDs(raw string) bool {
	raw = repairWrappedArxivIdentifiers(raw)
	dois, arxivs := map[string]bool{}, map[string]bool{}
	for _, match := range referenceDOIs(raw) {
		doi := NormalizeDOI(match)
		if doiPlaceholderPattern.MatchString(doi) {
			continue
		}
		if strings.HasPrefix(doi, "10.48550/arxiv.") {
			base, _ := NormalizeArxivID(strings.TrimPrefix(doi, "10.48550/arxiv."))
			if base != "" {
				arxivs[base] = true
			}
		} else {
			dois[doi] = true
		}
	}
	for _, match := range arxivExplicitPattern.FindAllStringSubmatch(raw, -1) {
		base, _ := NormalizeArxivID(match[1])
		arxivs[base] = true
	}
	return len(dois) > 1 || len(arxivs) > 1
}

var wrappedArxivIdentifierPattern = regexp.MustCompile(`(?i)(?:10\.48550/arxiv\.|arxiv\.org/(?:abs|pdf)/|arxiv\s*:\s*)\d{4}\.\s*(?:\d\s*){4,5}(?:v\s*\d+)?`)

func repairWrappedArxivIdentifiers(raw string) string {
	return wrappedArxivIdentifierPattern.ReplaceAllStringFunc(raw, func(match string) string { return strings.Join(strings.Fields(match), "") })
}

// A PDF can wrap a repeated DOI in its URL (p1 9-1472). If the
// complete DOI is also printed, retain that identity rather than treating
// its visibly wrapped repeat as a second paper.
func referenceDOIs(raw string) []string {
	matches := doiPattern.FindAllStringIndex(raw, -1)
	result := []string{}
	for _, match := range matches {
		candidate := NormalizeDOI(raw[match[0]:match[1]])
		wrapped := false
		tail := strings.TrimSpace(raw[match[1]:])
		for _, other := range matches {
			full := NormalizeDOI(raw[other[0]:other[1]])
			if len(full) > len(candidate) && strings.HasPrefix(full, candidate) && strings.HasPrefix(tail, strings.TrimPrefix(full, candidate)) {
				wrapped = true
				break
			}
		}
		if !wrapped {
			result = append(result, candidate)
		}
	}
	return result
}
