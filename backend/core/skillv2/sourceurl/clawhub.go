package sourceurl

import (
	"fmt"
	"net/url"
	"strings"
)

type ClawHubResolution struct {
	Coordinate  string
	DownloadURL string
}

// ResolveClawHubPageURL supports canonical /<owner>/skills/<slug> skill pages
// and their legacy /<owner>/<slug> links. Publisher identity is passed separately
// because ClawHub's download API resolves duplicate slugs using its owner query.
// Existing API URLs remain available to the caller as direct downloads.
func ResolveClawHubPageURL(parsed *url.URL) (resolution ClawHubResolution, matched bool, err error) {
	if parsed == nil {
		return ClawHubResolution{}, false, nil
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host != "clawhub.ai" {
		return ClawHubResolution{}, false, nil
	}
	if strings.HasPrefix(parsed.Path, "/api/") {
		return ClawHubResolution{}, false, nil
	}
	if parsed.User != nil || parsed.Port() != "" {
		return ClawHubResolution{}, true, fmt.Errorf("ClawHub URL must not contain credentials or a port")
	}
	parts, err := registryURLPathParts(parsed.EscapedPath(), "ClawHub", false)
	if err != nil {
		return ClawHubResolution{}, true, err
	}
	if !((len(parts) == 2 && parts[1] != "skills") || (len(parts) == 3 && parts[1] == "skills")) {
		return ClawHubResolution{}, true, fmt.Errorf("ClawHub URL must point to /<owner>/skills/<slug> or /<owner>/<slug>")
	}
	owner, slug := parts[0], parts[len(parts)-1]
	downloadURL := url.URL{Scheme: "https", Host: "clawhub.ai", Path: "/api/v1/download"}
	query := downloadURL.Query()
	query.Set("owner", owner)
	query.Set("slug", slug)
	if err := addRegistryDownloadSelection(parsed, query, "ClawHub"); err != nil {
		return ClawHubResolution{}, true, err
	}
	downloadURL.RawQuery = query.Encode()
	return ClawHubResolution{Coordinate: "@" + owner + "/" + slug, DownloadURL: downloadURL.String()}, true, nil
}
