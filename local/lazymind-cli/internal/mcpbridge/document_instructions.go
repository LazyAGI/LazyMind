package mcpbridge

import (
	"fmt"
	"strings"
)

// Keep document access guidance on the tools that use it, including the configured
// authorization base needed by clients that receive relative action URLs.
func documentToolInstructions(name, webBase string) string {
	if strings.HasPrefix(name, "cloud_document.") {
		return " Honor the user's chosen tool and account. Otherwise prefer capable host document tools. Use returned source_id and read_locator, pagination and versions. Only a read result supplies document content. Report unsupported formats and connection guidance. Permission denial requires authorization for the same account." +
			fmt.Sprintf(" Resolve relative authorization action URLs against %q, preserving its path prefix. Present the URL and retry the same source_id after authorization.", webBase)
	}
	if name == "knowledge.document.upload" {
		return " Supply absolute regular-file paths readable by the Connector. Resolve attachments, downloads or exports before uploading. Report the returned operation state. The user can request processing status later."
	}
	if name == "knowledge.document.list" || name == "knowledge.document.get" || name == "knowledge.search" {
		return " Read already ingested documents using their IDs. Use list name/path filters, get include_chunks, or search for content. Binary files require file transport."
	}
	return ""
}
