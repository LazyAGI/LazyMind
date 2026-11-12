package codex

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"lazymind/agentconnector/internal/chatagent"
)

// Native Windows drive paths are not URLs: url.Parse treats the drive as a scheme.
func pptLinkPath(target string) (string, bool) {
	if len(target) >= 3 && ((target[0] >= 'A' && target[0] <= 'Z') || (target[0] >= 'a' && target[0] <= 'z')) && target[1] == ':' && (target[2] == '\\' || target[2] == '/') {
		return target, true
	}
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "" && parsed.Scheme != "file") || parsed.Host != "" || strings.HasPrefix(target, "/static-files/") {
		return "", false
	}
	return parsed.Path, true
}

var pptLink = regexp.MustCompile(`\[([^\]\n]*)\]\((<[^>\n]+>|[^)\n]+)\)`)

// Transfer only explicitly linked PPT files from this conversation's workspace.
// The Core attachment endpoint emits a managed download link; the original
// host-local link is reduced to its label so it cannot navigate the web app.
func publishPPTLinks(text, workspace string, emit func(chatagent.Event) error) (string, error) {
	var publishErr error
	seen := map[string]bool{}
	result := pptLink.ReplaceAllStringFunc(text, func(link string) string {
		parts := pptLink.FindStringSubmatch(link)
		target := strings.Trim(strings.TrimSpace(parts[2]), "<>")
		localPath, ok := pptLinkPath(target)
		if !ok || !strings.EqualFold(filepath.Ext(localPath), ".pptx") {
			return link
		}
		if publishErr != nil {
			return link
		}
		root, err := filepath.EvalSymlinks(workspace)
		path := filepath.FromSlash(localPath)
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		relative, relErr := filepath.Rel(root, resolved)
		if err != nil || resolveErr != nil || relErr != nil || !filepath.IsLocal(relative) {
			// Workflow artifacts may live on a different host. Leave their
			// references for Core to resolve against the run's published artifacts.
			return link
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return link
		}
		if !seen[resolved] {
			publishErr = emit(chatagent.Event{Type: "attachment", Attachment: &chatagent.Attachment{
				Path: resolved, Filename: filepath.Base(resolved),
				MediaType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			}})
			seen[resolved] = true
		}
		return parts[1]
	})
	return result, publishErr
}
