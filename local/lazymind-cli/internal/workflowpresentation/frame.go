// Package workflowpresentation serves the shared external Workflow page wrapper.
package workflowpresentation

import (
	"bytes"
	"embed"
	"html"
)

//go:embed assets/presentation.html
var assets embed.FS

func HTML(frontendURL, downloads string) []byte {
	body, _ := assets.ReadFile("assets/presentation.html")
	body = bytes.ReplaceAll(body, []byte("__LAZYMIND_FRONTEND_URL__"), []byte(html.EscapeString(frontendURL)))
	return bytes.ReplaceAll(body, []byte("__LAZYMIND_DOWNLOAD_MODE__"), []byte(html.EscapeString(downloads)))
}
