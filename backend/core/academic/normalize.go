package academic

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	doiPattern           = regexp.MustCompile(`(?i)10\.\d{4,9}/[-._;()/:A-Z0-9]+`)
	arxivPattern         = regexp.MustCompile(`(?i)(?:arxiv\s*:\s*)?((?:\d{4}\.\d{4,5}|[a-z-]+/\d{7})(?:v(\d+))?)`)
	arxivExplicitPattern = regexp.MustCompile(`(?i)(?:arxiv\s*[:.]\s*|arxiv\s*\.\s*org\s*/\s*(?:abs|pdf)\s*/\s*)((?:\d{4}\.\s*\d{4,5}|[a-z-]+/\s*\d{7})(?:v\s*(\d+))?)`)
	yearPattern          = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
)

func NormalizeDOI(value string) string {
	v := strings.TrimSpace(strings.ToLower(value))
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "http://dx.doi.org/", "doi:"} {
		v = strings.TrimPrefix(v, prefix)
	}
	if match := doiPattern.FindString(v); match != "" {
		v = strings.ToLower(match)
	}
	return strings.TrimRight(v, ".,;)]}")
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
	if match := doiPattern.FindString(raw); match != "" {
		doi = NormalizeDOI(match)
	}
	if match := arxivExplicitPattern.FindStringSubmatch(raw); len(match) > 1 {
		arxiv, version = NormalizeArxivID(match[1])
	}
	return
}

func extractYear(raw string) int {
	v := yearPattern.FindString(raw)
	year, _ := strconv.Atoi(v)
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
