package sourceurl

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
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
	if parsed.User != nil || parsed.Port() != "" {
		return SkillHubResolution{}, true, fmt.Errorf("SkillHub URL must not contain credentials or a port")
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
	if err := addRegistryDownloadSelection(parsed, query, "SkillHub"); err != nil {
		return SkillHubResolution{}, true, err
	}
	downloadURL.RawQuery = query.Encode()
	return SkillHubResolution{Coordinate: coordinate, DownloadURL: downloadURL.String()}, true, nil
}

func skillHubURLPathParts(escapedPath string) ([]string, error) {
	// Keep spaces compatible with existing SkillHub links. The package metadata
	// validator reports an invalid runtime name if it must fall back to this slug.
	return registryURLPathParts(escapedPath, "SkillHub", true)
}

func registryURLPathParts(escapedPath, provider string, allowSpaces bool) ([]string, error) {
	rawParts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(escapedPath, "/"), "/"), "/")
	parts := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, err := url.PathUnescape(rawPart)
		if err != nil || part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\\?#@&=:`) || strings.ContainsFunc(part, func(r rune) bool { return unicode.IsControl(r) || (!allowSpaces && unicode.IsSpace(r)) }) {
			return nil, fmt.Errorf("%s URL contains an invalid path segment", provider)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// Website tracking parameters and fragments do not select a skill. Only forward
// the registry's supported version selectors, after validating them separately.
func addRegistryDownloadSelection(parsed *url.URL, query url.Values, provider string) error {
	input, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return fmt.Errorf("%s URL contains an invalid query", provider)
	}
	for _, key := range []string{"version", "tag"} {
		values, present := input[key]
		if !present {
			continue
		}
		if len(values) != 1 || values[0] == "" || strings.ContainsFunc(values[0], func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._+-", r))
		}) {
			return fmt.Errorf("%s URL contains an invalid %s selector", provider, key)
		}
		query.Set(key, values[0])
	}
	if query.Has("version") && query.Has("tag") {
		return fmt.Errorf("%s URL must select either a version or a tag", provider)
	}
	return nil
}
