package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lazymind/core/academic/arxivsource"
)

func lookupPaperLink(ctx context.Context, input string) (WorkInput, error) {
	if _, err := arxivsource.Parse(input); err == nil {
		metadata, err := arxivsource.NewClient().Lookup(ctx, input)
		if err != nil {
			return WorkInput{}, err
		}
		return arxivWorkInput(metadata), nil
	}
	doi := NormalizeDOI(input)
	if !doiPattern.MatchString(doi) {
		return WorkInput{}, errors.New("请输入 DOI 或 arXiv 编号、链接")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.crossref.org/works/"+url.PathEscape(doi), nil)
	if err != nil {
		return WorkInput{}, err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return WorkInput{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return WorkInput{}, fmt.Errorf("DOI 查询失败（HTTP %d）", response.StatusCode)
	}
	var result struct {
		Message struct {
			Title         []string `json:"title"`
			CitationCount *int64   `json:"is-referenced-by-count"`
			Authors       []struct {
				Given  string `json:"given"`
				Family string `json:"family"`
			} `json:"author"`
			Venue  []string `json:"container-title"`
			Issued struct {
				DateParts [][]int `json:"date-parts"`
			} `json:"issued"`
			DOI string `json:"DOI"`
		} `json:"message"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return WorkInput{}, err
	}
	m := result.Message
	if len(m.Title) == 0 || strings.TrimSpace(m.Title[0]) == "" || NormalizeDOI(m.DOI) != doi {
		return WorkInput{}, errors.New("DOI 服务未返回匹配的有效题录")
	}
	work := WorkInput{CitationCount: m.CitationCount, Title: m.Title[0], DOI: doi, Provider: "crossref", ProviderWorkID: doi, Provenance: map[string]any{}}
	if work.CitationCount != nil && *work.CitationCount < 0 {
		work.CitationCount = nil
	}
	for _, author := range m.Authors {
		if name := strings.TrimSpace(author.Given + " " + author.Family); name != "" {
			work.Authors = append(work.Authors, name)
		}
	}
	if len(m.Venue) > 0 {
		work.Venue = m.Venue[0]
	}
	if len(m.Issued.DateParts) > 0 && len(m.Issued.DateParts[0]) > 0 {
		work.Year = m.Issued.DateParts[0][0]
	}
	if pdf, license, version, e := resolveDOIOpenAccess(ctx, doi); e != nil {
		work.FulltextError = fulltextLookupDiagnostic(e)
	} else if pdf != "" {
		work.Provenance["pdf_url"], work.Provenance["pdf_license"], work.Provenance["pdf_version"] = pdf, license, version
		work.Provenance["pdf_provider"] = "unpaywall"
	}
	return work, nil
}
