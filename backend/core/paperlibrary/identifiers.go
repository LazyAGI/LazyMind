package paperlibrary

import (
	"regexp"
	"strings"
)

var arxivVersion = regexp.MustCompile(`(?i)v[1-9][0-9]*$`)

func ArxivBase(value string) string {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".pdf")
	for _, prefix := range []string{"https://arxiv.org/abs/", "http://arxiv.org/abs/", "https://arxiv.org/pdf/", "http://arxiv.org/pdf/", "arxiv:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	return arxivVersion.ReplaceAllString(value, "")
}

func DOI(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/", "doi:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	return strings.TrimRight(value, ".,;)]}")
}
