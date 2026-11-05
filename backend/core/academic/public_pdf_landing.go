package academic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func probePublicPDFCandidate(ctx context.Context, client *http.Client, candidate FulltextCandidate, doi string) (string, error) {
	strictPublication := candidate.SourceType == "published_landing"
	if officialArxivID(candidate.URL) != "" {
		if strictPublication {
			return "", fmt.Errorf("arXiv 预印本不能替代正式发表版")
		}
		return withArxivSource(ctx, func() (string, error) { return probePaperPDF(ctx, client, candidate.URL) })
	}
	if candidate.SourceType != "open_access_landing" && !strictPublication {
		return probePaperPDF(ctx, client, candidate.URL)
	}
	resp, err := fetchPaperSource(ctx, client, candidate.URL, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	if !strictPublication && bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("%PDF-")) {
		return resp.Request.URL.String(), nil
	}
	body, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(prefix), resp.Body), (2<<20)+1))
	if err != nil {
		return "", err
	}
	if len(body) > 2<<20 {
		return "", pdfLookupError("论文页面超过大小限制")
	}
	links, err := publicLandingPDFURLs(body, resp.Request.URL, doi, false, strictPublication)
	if err != nil {
		return "", err
	}
	if len(links) == 0 {
		return "", fmt.Errorf("论文页面未提供可用 PDF 地址")
	}
	address, err := probePaperPDF(ctx, client, links[0])
	if err == nil && strictPublication {
		u, err := url.Parse(address)
		if err != nil || u.Hostname() != resp.Request.URL.Hostname() {
			return "", fmt.Errorf("出版社 PDF 地址转向了其他来源")
		}
	}
	return address, err
}

func publicLandingPDFURL(body []byte, base *url.URL, doi string) (string, error) {
	links, err := publicLandingPDFURLs(body, base, doi, false)
	if err != nil {
		return "", err
	}
	if len(links) == 0 {
		return "", fmt.Errorf("论文页面未提供可用 PDF 地址")
	}
	return links[0], nil
}

// Import probes accept publisher metadata; research can additionally inspect
// ordinary PDF links, whose downloaded bodies still require identity validation.
func publicLandingPDFURLs(body []byte, base *url.URL, doi string, includeLinks bool, requireDOI ...bool) ([]string, error) {
	tree, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	links := []string{}
	seen := map[string]bool{}
	strict := len(requireDOI) > 0 && requireDOI[0]
	mismatch, verified := false, false
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "meta" || includeLinks && n.Data == "a") {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[strings.ToLower(a.Key)] = strings.TrimSpace(a.Val)
			}
			name := strings.ToLower(firstNonEmpty(attrs["name"], attrs["property"]))
			if n.Data == "meta" && (name == "citation_doi" || name == "dc.identifier.doi") {
				if found := NormalizeDOI(attrs["content"]); doi != "" && found != "" {
					verified = verified || found == NormalizeDOI(doi)
					mismatch = mismatch || found != NormalizeDOI(doi)
				}
			}
			address := ""
			if n.Data == "meta" && name == "citation_pdf_url" {
				address = attrs["content"]
			}
			if n.Data == "a" && strings.Contains(strings.ToLower(attrs["href"]), ".pdf") {
				address = attrs["href"]
			}
			if address != "" {
				if u, err := url.Parse(address); err == nil {
					u = base.ResolveReference(u)
					u.Fragment = ""
					if zoteroAllowedURL(u, false) && (!strict || u.Hostname() == base.Hostname()) && !seen[u.String()] && len(links) < 5 {
						seen[u.String()] = true
						links = append(links, u.String())
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(tree)
	if mismatch {
		return nil, fmt.Errorf("论文页面 DOI 与目标文献不一致")
	}
	if strict && !verified {
		return nil, fmt.Errorf("论文页面未声明目标 DOI")
	}
	return links, nil
}
