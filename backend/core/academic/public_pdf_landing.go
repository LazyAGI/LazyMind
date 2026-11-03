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
	if candidate.SourceType != "open_access_landing" {
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
	if bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("%PDF-")) {
		return resp.Request.URL.String(), nil
	}
	body, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(prefix), resp.Body), (2<<20)+1))
	if err != nil {
		return "", err
	}
	if len(body) > 2<<20 {
		return "", pdfLookupError("论文页面超过大小限制")
	}
	address, err := publicLandingPDFURL(body, resp.Request.URL, doi)
	if err != nil {
		return "", err
	}
	return probePaperPDF(ctx, client, address)
}

func publicLandingPDFURL(body []byte, base *url.URL, doi string) (string, error) {
	tree, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	address := ""
	mismatch := false
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			name, value := "", ""
			for _, a := range n.Attr {
				if a.Key == "name" || a.Key == "property" {
					name = strings.ToLower(a.Val)
				}
				if a.Key == "content" {
					value = strings.TrimSpace(a.Val)
				}
			}
			if name == "citation_doi" || name == "dc.identifier.doi" {
				if NormalizeDOI(value) != "" && NormalizeDOI(value) != NormalizeDOI(doi) {
					mismatch = true
				}
			}
			if name == "citation_pdf_url" && address == "" {
				u, e := url.Parse(value)
				if e == nil {
					u = base.ResolveReference(u)
					u.Fragment = ""
					if zoteroAllowedURL(u, false) {
						address = u.String()
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
		return "", fmt.Errorf("论文页面 DOI 与目标文献不一致")
	}
	if address == "" {
		return "", fmt.Errorf("论文页面未提供可用 PDF 地址")
	}
	return address, nil
}
