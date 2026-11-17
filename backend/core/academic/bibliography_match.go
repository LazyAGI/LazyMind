package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// searchCrossrefTitle returns candidates without requiring authors or publication year.
// Matching and ambiguity handling are shared with the OpenAlex title resolver.
func searchCrossrefTitle(ctx context.Context, title string) ([]WorkInput, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u := "https://api.crossref.org/works?rows=10&query.title=" + url.QueryEscape(title)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Crossref 题录匹配失败（HTTP %d）", resp.StatusCode)
	}
	var data struct {
		Message struct {
			Items []struct {
				Type   string   `json:"type"`
				DOI    string   `json:"DOI"`
				Title  []string `json:"title"`
				Venue  []string `json:"container-title"`
				Author []struct {
					Given  string `json:"given"`
					Family string `json:"family"`
				} `json:"author"`
				Issued struct {
					DateParts [][]int `json:"date-parts"`
				} `json:"issued"`
			} `json:"items"`
		} `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&data); err != nil {
		return nil, err
	}
	works := []WorkInput{}
	for _, item := range data.Message.Items {
		if len(item.Title) == 0 || item.Type == "component" {
			continue
		}
		doi := NormalizeDOI(item.DOI)
		if !doiPattern.MatchString(doi) {
			continue
		}
		w := WorkInput{Title: item.Title[0], DOI: doi, Provider: "crossref", ProviderWorkID: doi}
		for _, a := range item.Author {
			if name := strings.TrimSpace(a.Given + " " + a.Family); name != "" {
				w.Authors = append(w.Authors, name)
			}
		}
		if len(item.Venue) > 0 {
			w.Venue = item.Venue[0]
		}
		if len(item.Issued.DateParts) > 0 && len(item.Issued.DateParts[0]) > 0 {
			w.Year = item.Issued.DateParts[0][0]
		}
		works = append(works, w)
	}
	return works, nil
}
