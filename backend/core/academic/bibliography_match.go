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

	"lazymind/core/paperlibrary"
)

// A title alone cannot authorize attaching a PDF. Require an unambiguous exact
// normalized title, year and first-author match before accepting a discovered DOI.
func findBibliographyDOI(ctx context.Context, m paperlibrary.Metadata) (string, error) {
	if m.Year == 0 || len(m.Authors) == 0 {
		return "", errors.New("题录已保存；缺少 DOI/arXiv 编号，且作者或年份不足以可靠匹配，请补充后重试")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u := "https://api.crossref.org/works?rows=5&query.bibliographic=" + url.QueryEscape(fmt.Sprintf("%s %s %d", m.Title, m.Authors[0], m.Year))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Crossref 题录匹配失败（HTTP %d）", resp.StatusCode)
	}
	var data struct {
		Message struct {
			Items []struct {
				DOI    string   `json:"DOI"`
				Title  []string `json:"title"`
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
		return "", err
	}
	matches := map[string]bool{}
	for _, item := range data.Message.Items {
		if len(item.Title) == 0 || NormalizeTitle(item.Title[0]) != NormalizeTitle(m.Title) || len(item.Author) == 0 || len(item.Issued.DateParts) == 0 || len(item.Issued.DateParts[0]) == 0 || item.Issued.DateParts[0][0] != m.Year {
			continue
		}
		a := item.Author[0]
		first := normalizeAuthor(m.Authors[0])
		if first != normalizeAuthor(strings.TrimSpace(a.Given+" "+a.Family)) && first != normalizeAuthor(strings.TrimSpace(a.Family+" "+a.Given)) {
			continue
		}
		doi := NormalizeDOI(item.DOI)
		if doiPattern.MatchString(doi) {
			matches[doi] = true
		}
	}
	if len(matches) != 1 {
		return "", errors.New("题录已保存；在线匹配不唯一或置信度不足，请补充 DOI/arXiv 编号后重试，未自动关联 PDF")
	}
	for doi := range matches {
		return doi, nil
	}
	return "", errors.New("未找到匹配题录")
}
