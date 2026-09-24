package taskdisplay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strings"
)

func Text(value string, limit int) string {
	r := []rune(strings.TrimSpace(value))
	if len(r) > limit {
		r = r[:limit]
	}
	return string(r)
}
func StableID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

// PublicURL admits public HTTP(S) links without credentials or signed query values.
func PublicURL(raw string) string {
	if len(raw) > 2048 {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || (ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast())) {
		return ""
	}
	for key := range u.Query() {
		k := strings.ToLower(key)
		if strings.Contains(k, "token") || strings.Contains(k, "signature") || strings.Contains(k, "credential") || strings.Contains(k, "api_key") || k == "key" || k == "sig" || k == "authorization" || k == "password" {
			return ""
		}
	}
	u.Fragment = ""
	return u.String()
}

// Sources only copies explicitly public fields. Raw search content is not a summary.
func NormalizeSources(raw json.RawMessage) []PublicSource {
	result := []PublicSource{}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return result
	}
	seen := map[string]bool{}
	for _, item := range items {
		str := func(key string) string { v, _ := item[key].(string); return v }
		source := PublicSource{Title: Text(str("title"), 100), Snippet: Text(str("snippet"), 300), Kind: "web"}
		link := PublicURL(str("url"))
		if link != "" {
			u, _ := url.Parse(link)
			domain := u.Hostname()
			source.URL = link
			source.Domain = &domain
			source.Platform = "网页"
			if domain == "github.com" || domain == "gitlab.com" {
				source.Kind = "repository"
				source.Platform = domain
			}
			if source.Title == "" {
				source.Title = domain
			}
			source.SourceID = StableID(link)
		} else {
			id := str("resource_id")
			if id == "" {
				id = str("document_id")
			}
			if id == "" || len(id) > 128 || !publicID.MatchString(id) {
				continue
			}
			source.Kind = "knowledge"
			source.ResourceID = id
			source.DatasetID = Text(str("dataset_id"), 128)
			source.Platform = "知识库"
			if source.Title == "" {
				source.Title = Text(str("file_name"), 100)
			}
			if source.Title == "" {
				source.Title = "文档"
			}
			source.SourceID = StableID(source.DatasetID + ":" + id)
		}
		if !seen[source.SourceID] {
			seen[source.SourceID] = true
			result = append(result, source)
		}
	}
	return result
}
