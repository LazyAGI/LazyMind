package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type paperSearchRequest struct {
	ExpansionID         string   `json:"expansion_id"`
	Query               string   `json:"query"`
	MaxResults          int      `json:"max_results"`
	Sources             []string `json:"sources"`
	Mode                string   `json:"mode"`
	PaperID             string   `json:"paper_id"`
	Cursor              string   `json:"cursor"`
	PreparationRevision *int64   `json:"preparation_revision"`
	position            searchPosition
	apiKey              string
	toolConfig          map[string]any
	scope               string
}
type discoveredPaper struct {
	Work       WorkInput
	Version    string
	PaperURL   string
	Candidates []FulltextCandidate
}
type discoveryLead struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}
type searchOutcome struct {
	Source        string     `json:"source"`
	Status        string     `json:"status"`
	Count         int        `json:"count"`
	ElapsedMS     int64      `json:"elapsed_ms"`
	NextCursor    string     `json:"next_cursor,omitempty"`
	Limitation    string     `json:"limitation,omitempty"`
	Reused        bool       `json:"reused,omitempty"`
	PaperCount    int        `json:"paper_count"`
	LeadCount     int        `json:"lead_count"`
	NewPaperCount int        `json:"new_paper_count"`
	TotalCount    *int       `json:"total_count,omitempty"`
	HasMore       bool       `json:"has_more"`
	RetryAt       *time.Time `json:"retry_at,omitempty"`
}

type searchPosition struct {
	Offset int    `json:"offset,omitempty"`
	Page   int    `json:"page,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type discoveredPage struct {
	Papers     []discoveredPaper
	Leads      []discoveryLead
	TotalCount *int
	Next       *searchPosition
}
type scholarlyLocation struct {
	PDFURL     string `json:"pdf_url"`
	LandingURL string `json:"landing_page_url"`
	Version    string `json:"version"`
	License    string `json:"license"`
}
type scholarlyWork struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	DOI         string `json:"doi"`
	Year        int    `json:"publication_year"`
	Citations   *int64 `json:"cited_by_count"`
	Authorships []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	Abstract  map[string][]int    `json:"abstract_inverted_index"`
	Locations []scholarlyLocation `json:"locations"`
	Best      *scholarlyLocation  `json:"best_oa_location"`
	Primary   *struct {
		Source *struct {
			DisplayName string `json:"display_name"`
		} `json:"source"`
	} `json:"primary_location"`
	ContentURLs struct {
		PDF string `json:"pdf"`
	} `json:"content_urls"`
	References []string `json:"referenced_works"`
}

var errOpenAlexNotFound = errors.New("OpenAlex 未收录该论文")

func openAlexGet(ctx context.Context, path string, params url.Values, key string, out any) error {
	if key != "" {
		params.Set("api_key", key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openalex.org/"+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "LazyMind academic research/1.0 (mailto:opensource@lazyagi.org)")
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return errors.New("OpenAlex 暂时不可用")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return errOpenAlexNotFound
	}
	if response.StatusCode != 200 {
		return scholarlyHTTPFailure(response.StatusCode, response.Header, response.Body)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(out)
}
func scholarlyPaper(item scholarlyWork) discoveredPaper {
	work := WorkInput{Title: item.Title, Year: item.Year, DOI: NormalizeDOI(item.DOI), Provider: "openalex", ProviderWorkID: strings.TrimPrefix(item.ID, "https://openalex.org/"), CitationCount: item.Citations, Provenance: map[string]any{}, TrustedFulltext: true}
	for _, author := range item.Authorships {
		if author.Author.DisplayName != "" {
			work.Authors = append(work.Authors, author.Author.DisplayName)
		}
	}
	type word struct {
		position int
		text     string
	}
	words := []word{}
	for term, positions := range item.Abstract {
		for _, position := range positions {
			words = append(words, word{position, term})
		}
	}
	sort.Slice(words, func(i, j int) bool { return words[i].position < words[j].position })
	ordered := []string{}
	for _, w := range words {
		ordered = append(ordered, w.text)
	}
	work.Abstract = strings.Join(ordered, " ")
	if item.Primary != nil && item.Primary.Source != nil {
		work.Venue = item.Primary.Source.DisplayName
	}
	if strings.HasPrefix(work.DOI, "10.48550/arxiv.") {
		work.ArxivID, _ = NormalizeArxivID(strings.TrimPrefix(work.DOI, "10.48550/arxiv."))
	}
	result := discoveredPaper{Work: work, PaperURL: item.ID}
	locations := item.Locations
	if item.Best != nil {
		locations = append([]scholarlyLocation{*item.Best}, locations...)
	}
	seen := map[string]bool{}
	for _, location := range locations {
		if location.PDFURL != "" && !seen[location.PDFURL] {
			seen[location.PDFURL] = true
			result.Candidates = append(result.Candidates, FulltextCandidate{URL: location.PDFURL, SourceType: "open_access_pdf", SourceProvider: "openalex", Version: location.Version, License: location.License, ExpectedMIME: "application/pdf"})
		}
		if result.PaperURL == item.ID && location.LandingURL != "" {
			result.PaperURL = location.LandingURL
		}
	}
	if address := item.ContentURLs.PDF; address != "" {
		if u, e := url.Parse(address); e == nil && u.Scheme == "https" && u.Host == "content.openalex.org" && u.User == nil {
			query := u.Query()
			query.Del("api_key")
			u.RawQuery = query.Encode()
			result.Candidates = append(result.Candidates, FulltextCandidate{URL: u.String(), SourceType: "openalex_hosted", SourceProvider: "openalex", ExpectedMIME: "application/pdf"})
		}
	}
	if len(result.Candidates) > 0 {
		c := result.Candidates[0]
		result.Version = c.Version
		result.Work.Provenance["pdf_url"] = c.URL
		result.Work.Provenance["pdf_version"] = c.Version
	}
	return result
}
func searchOpenAlex(ctx context.Context, record orm.AcademicExpansion, request paperSearchRequest) (discoveredPage, error) {
	key := request.apiKey
	params := url.Values{"per_page": {fmt.Sprint(request.MaxResults)}}
	if key != "" {
		params.Set("cursor", firstNonEmpty(request.position.Cursor, "*"))
	} else {
		params.Set("page", fmt.Sprint(request.position.Page+1))
	}
	if request.Mode == "references" || request.Mode == "citations" {
		source, err := expansionSource(store.DB().WithContext(ctx), record, request.PaperID)
		if err != nil {
			return discoveredPage{}, err
		}
		var work orm.AcademicWork
		if err = store.DB().WithContext(ctx).Where("id=?", source.Paper.PaperID).Take(&work).Error; err != nil {
			return discoveredPage{}, err
		}
		external := map[string]string{}
		_ = json.Unmarshal(work.ExternalIDsJSON, &external)
		id := external["openalex"]
		if id == "" {
			doi := source.Paper.DOI
			if doi == "" && source.Paper.ArxivID != "" {
				doi = "10.48550/arxiv." + source.Paper.ArxivID
			}
			if doi == "" {
				return discoveredPage{}, errors.New("该论文尚无可用的引用索引身份")
			}
			id = "https://doi.org/" + doi
		}
		var target scholarlyWork
		if err = openAlexGet(ctx, "works/"+url.PathEscape(id), url.Values{}, key, &target); err != nil {
			return discoveredPage{}, err
		}
		if request.Mode == "citations" {
			params.Set("filter", "cites:"+strings.TrimPrefix(target.ID, "https://openalex.org/"))
		} else {
			params.Set("filter", "cited_by:"+strings.TrimPrefix(target.ID, "https://openalex.org/"))
		}
	} else {
		params.Set("search", request.Query)
	}
	var response struct {
		Results []scholarlyWork `json:"results"`
		Meta    struct {
			NextCursor string `json:"next_cursor"`
			Count      *int   `json:"count"`
		} `json:"meta"`
	}
	if err := openAlexGet(ctx, "works", params, key, &response); err != nil {
		return discoveredPage{}, err
	}
	result := []discoveredPaper{}
	for _, item := range response.Results {
		if strings.TrimSpace(item.Title) != "" {
			result = append(result, scholarlyPaper(item))
		}
	}
	page := discoveredPage{Papers: result, TotalCount: response.Meta.Count}
	if len(response.Results) > 0 {
		if key != "" && response.Meta.NextCursor != "" {
			page.Next = &searchPosition{Cursor: response.Meta.NextCursor}
		} else if key == "" && response.Meta.Count != nil && (request.position.Page+1)*request.MaxResults < *response.Meta.Count && (request.position.Page+2)*request.MaxResults <= 10000 {
			page.Next = &searchPosition{Page: request.position.Page + 1}
		}
	}
	return page, nil
}
func externalPaperSearch(ctx context.Context, user, source, query string, limit int) ([]discoveredPaper, []discoveryLead, error) {
	request := paperSearchRequest{Query: query, MaxResults: limit, Sources: []string{source}, Mode: "keyword"}
	if err := configureSearchSource(ctx, user, source, &request); err != nil {
		return nil, nil, err
	}
	page, outcome, err := fetchPaperPage(ctx, orm.AcademicExpansion{}, source, request)
	if err != nil {
		return nil, nil, err
	}
	if outcome.Status != "ok" && outcome.Status != "empty" {
		return nil, nil, &searchFailure{Status: outcome.Status, RetryAt: outcome.RetryAt}
	}
	return page.Papers, page.Leads, nil
}

func searchExternalPage(ctx context.Context, source string, request paperSearchRequest) (discoveredPage, error) {
	var response struct {
		Items []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Extra   struct {
				Abstract string   `json:"abstract"`
				DOI      string   `json:"doi"`
				DocID    string   `json:"doc_id"`
				Authors  []string `json:"authors"`
				Year     int      `json:"year"`
				Venue    string   `json:"venue"`
			} `json:"extra"`
		} `json:"items"`
		TotalCount *int   `json:"total_count"`
		TotalPages *int   `json:"total_pages"`
		Page       int    `json:"page"`
		NextCursor string `json:"next_cursor"`
	}
	if err := internalAcademic(ctx, "search", map[string]any{"source": source, "query": request.Query, "limit": request.MaxResults, "page": request.position.Page + 1, "cursor": request.position.Cursor, "tool_config": request.toolConfig}, &response); err != nil {
		return discoveredPage{}, err
	}
	papers := []discoveredPaper{}
	leads := []discoveryLead{}
	for _, item := range response.Items {
		if source == "web" {
			// Only identifier-backed web hits can register papers. Project/repository
			// pages remain useful leads, never model-authored bibliographic identities.
			id := officialArxivID(item.URL)
			doi := NormalizeDOI(item.URL)
			if id != "" || doiPattern.MatchString(doi) {
				work, e := lookupPaperLink(ctx, firstNonEmpty(id, doi))
				if e == nil {
					papers = append(papers, discoveredPaper{Work: work, PaperURL: item.URL})
					continue
				}
			}
			leads = append(leads, discoveryLead{item.Title, item.URL, item.Snippet})
			continue
		}
		if item.Title == "" {
			continue
		}
		work := WorkInput{Title: item.Title, Authors: item.Extra.Authors, Year: item.Extra.Year, Venue: item.Extra.Venue, DOI: item.Extra.DOI, Abstract: item.Extra.Abstract, Provider: "sciverse", ProviderWorkID: item.Extra.DocID, Provenance: map[string]any{"paper_url": item.URL}}
		papers = append(papers, discoveredPaper{Work: work, PaperURL: item.URL})
	}
	page := discoveredPage{Papers: papers, Leads: leads, TotalCount: response.TotalCount}
	if source == "sciverse" && len(response.Items) > 0 {
		if response.NextCursor != "" {
			page.Next = &searchPosition{Cursor: response.NextCursor}
		} else if response.TotalPages != nil && response.Page > 0 && response.Page < *response.TotalPages {
			page.Next = &searchPosition{Page: response.Page}
		}
	}
	return page, nil
}

func discoverPaperPage(ctx context.Context, record orm.AcademicExpansion, source string, request paperSearchRequest) (discoveredPage, error) {
	switch source {
	case "openalex":
		return searchOpenAlex(ctx, record, request)
	case "arxiv":
		result, err := arxivsource.NewClient().SearchPage(ctx, request.Query, request.MaxResults, request.position.Offset)
		if err != nil {
			return discoveredPage{}, err
		}
		page := discoveredPage{TotalCount: result.TotalResults}
		for _, m := range result.Items {
			page.Papers = append(page.Papers, discoveredPaper{Work: arxivWorkInput(m), Version: m.Version, PaperURL: "https://arxiv.org/abs/" + m.BaseID})
		}
		if result.NextStart != nil {
			page.Next = &searchPosition{Offset: *result.NextStart}
		}
		return page, nil
	case "sciverse", "web":
		return searchExternalPage(ctx, source, request)
	default:
		return discoveredPage{}, errors.New("unsupported_source")
	}
}
