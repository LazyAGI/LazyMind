package sourceurl

import (
	"fmt"
	"net/url"
	"strings"
)

const skillHubAPIHost = "api.skillhub.cn"

type SkillHubResolution struct {
	Coordinate  string
	DownloadURL string
}

// ResolveSkillHubPageURL recognizes a SkillHub page URL and resolves it to the
// corresponding package download endpoint. Non-SkillHub URLs are left to the
// caller by returning matched=false.
func ResolveSkillHubPageURL(parsed *url.URL) (resolution SkillHubResolution, matched bool, err error) {
	if parsed == nil {
		return SkillHubResolution{}, false, nil
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host != "skillhub.cn" {
		return SkillHubResolution{}, false, nil
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return SkillHubResolution{}, true, fmt.Errorf("SkillHub URL must not contain credentials, port, query, or fragment")
	}
	parts, err := skillHubURLPathParts(parsed.EscapedPath())
	if err != nil {
		return SkillHubResolution{}, true, err
	}
	if (len(parts) != 2 && len(parts) != 3) || parts[0] != "skills" {
		return SkillHubResolution{}, true, fmt.Errorf("SkillHub URL must point to /skills/<slug> or /skills/<namespace>/<slug>")
	}
	coordinate := parts[1]
	if len(parts) == 3 {
		coordinate = "@" + parts[1] + "/" + parts[2]
	}
	downloadURL := url.URL{Scheme: "https", Host: skillHubAPIHost, Path: "/api/v1/download"}
	query := downloadURL.Query()
	query.Set("slug", coordinate)
	downloadURL.RawQuery = query.Encode()
	return SkillHubResolution{Coordinate: coordinate, DownloadURL: downloadURL.String()}, true, nil
}

func skillHubURLPathParts(escapedPath string) ([]string, error) {
	rawParts := strings.Split(strings.Trim(escapedPath, "/"), "/")
	parts := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, err := url.PathUnescape(rawPart)
		if err != nil || part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\\`) || strings.ContainsRune(part, 0) {
			return nil, fmt.Errorf("SkillHub URL contains an invalid path segment")
		}
		parts = append(parts, part)
	}
	return parts, nil
}
