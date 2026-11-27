package workflowmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
)

// Keep this bounded manifest in the immutable MD snapshot, not in model instructions.
type markdownDependency struct {
	Source     string `json:"source"`
	ResourceID string `json:"resource_id"`
}
type markdownManifest struct {
	Images   []markdownDependency `json:"images"`
	Warnings []string             `json:"warnings"`
}

var markdownImageResourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var markdownDefinition = regexp.MustCompile(`(?m)^ {0,3}\[([^\]]+)\]:\s*(<[^>]+>|\S+)`)

func markdownImageReferences(text string) []string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			if fence == "" {
				fence = trim[:3]
			} else if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			lines[i] = ""
			continue
		}
		if fence != "" {
			lines[i] = ""
			continue
		}
		// Inline code is not an image dependency.
		for start := strings.IndexByte(line, '`'); start >= 0; start = strings.IndexByte(line, '`') {
			end := start + 1
			for end < len(line) && line[end] == '`' {
				end++
			}
			marker := line[start:end]
			close := strings.Index(line[end:], marker)
			if close < 0 {
				break
			}
			line = line[:start] + strings.Repeat(" ", end+close+len(marker)-start) + line[end+close+len(marker):]
		}
		lines[i] = line
	}
	text = strings.Join(lines, "\n")
	definitions := map[string]string{}
	for _, match := range markdownDefinition.FindAllStringSubmatch(text, -1) {
		definitions[strings.ToLower(strings.TrimSpace(match[1]))] = strings.Trim(match[2], "<>")
	}
	seen := map[string]bool{}
	result := []string{}
	for offset := 0; offset < len(text) && len(result) < 32; {
		next := strings.Index(text[offset:], "![")
		if next < 0 {
			break
		}
		start := offset + next
		if start > 0 && text[start-1] == '\\' {
			offset = start + 2
			continue
		}
		close := strings.IndexByte(text[start+2:], ']')
		if close < 0 {
			break
		}
		close += start + 2
		label := text[start+2 : close]
		offset = close + 1
		source := ""
		if offset < len(text) && text[offset] == '(' {
			pos := offset + 1
			begin := pos
			depth := 1
			angle := pos < len(text) && text[pos] == '<'
			if angle {
				begin++
				pos++
			}
			for pos < len(text) {
				ch := text[pos]
				if ch == '\\' && pos+1 < len(text) {
					pos += 2
					continue
				}
				if angle {
					if ch == '>' {
						source = text[begin:pos]
						break
					}
				} else {
					if ch == '(' {
						depth++
					}
					if ch == ')' {
						depth--
						if depth == 0 {
							source = text[begin:pos]
							break
						}
					}
					if (ch == ' ' || ch == '\n' || ch == '\t') && depth == 1 {
						source = text[begin:pos]
						break
					}
				}
				pos++
			}
			offset = pos + 1
		} else {
			if offset < len(text) && text[offset] == '[' {
				end := strings.IndexByte(text[offset+1:], ']')
				if end >= 0 {
					if end > 0 {
						label = text[offset+1 : offset+1+end]
					}
					offset += end + 2
				}
			}
			source = definitions[strings.ToLower(strings.TrimSpace(label))]
		}
		source = strings.TrimSpace(source)
		parsed, _ := url.Parse(source)
		if source == "" || strings.HasPrefix(source, "//") || parsed != nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "data" || parsed.Scheme == "asset") {
			continue
		}
		if !seen[source] {
			seen[source] = true
			result = append(result, source)
		}
	}
	return result
}

func localMarkdownImage(document, reference string) (localFile, error) {
	source := reference
	if strings.HasPrefix(strings.ToLower(source), "file:") {
		parsed, err := url.Parse(source)
		if err != nil || parsed.Host != "" && parsed.Host != "localhost" {
			return localFile{}, fmt.Errorf("not a local file URI")
		}
		source = parsed.Path
		if filepath.Separator == '\\' && len(source) > 2 && source[0] == '/' && source[2] == ':' {
			source = source[1:]
		}
		source = filepath.FromSlash(source)
	} else {
		if index := strings.IndexByte(source, '#'); index >= 0 {
			source = source[:index]
		}
		decoded, err := url.PathUnescape(source)
		if err != nil {
			return localFile{}, err
		}
		source = decoded
	}
	if !filepath.IsAbs(source) {
		source = filepath.Join(filepath.Dir(document), source)
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return localFile{}, err
	}
	document, err = filepath.EvalSymlinks(document)
	if err != nil {
		return localFile{}, err
	}
	roots := []string{filepath.Dir(document)}
	config, err := connectorconfig.Current()
	if err != nil {
		return localFile{}, err
	}
	if config != nil {
		roots = append(roots, config.WorkflowInputRoots...)
	}
	allowed := false
	for _, root := range roots {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		relative, relErr := filepath.Rel(root, canonical)
		if relErr == nil && filepath.IsLocal(relative) {
			allowed = true
			break
		}
	}
	if !allowed {
		return localFile{}, fmt.Errorf("image outside authorized directories")
	}
	file, err := readLocalInputFile(canonical)
	if err == nil && !strings.HasPrefix(file.MIMEType, "image/") {
		err = fmt.Errorf("not an image")
	}
	return file, err
}

func (c *Client) prepareMarkdownInput(ctx context.Context, path string, file localFile) localFile {
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".md" && ext != ".markdown" {
		return file
	}
	raw, err := base64.StdEncoding.DecodeString(file.Base64)
	if err != nil {
		return file
	}
	refs := markdownImageReferences(string(raw))
	if len(refs) == 0 {
		return file
	}
	optional, cancel := context.WithTimeout(ctx, time.Duration(max(30, min(180, len(refs)*10)))*time.Second)
	defer cancel()
	manifest := markdownManifest{Images: []markdownDependency{}, Warnings: []string{}}
	imported := map[string]string{}
	for _, reference := range refs {
		if optional.Err() != nil {
			manifest.Warnings = append(manifest.Warnings, "引用图片收集超时，已使用成功导入的图片。")
			break
		}
		type imageRead struct {
			file localFile
			err  error
		}
		reads := make(chan imageRead, 1)
		go func(ref string) { image, readErr := localMarkdownImage(path, ref); reads <- imageRead{image, readErr} }(reference)
		readCtx, stopRead := context.WithTimeout(optional, 5*time.Second)
		select {
		case <-readCtx.Done():
			stopRead()
			manifest.Warnings = append(manifest.Warnings, "图片读取超时："+reference)
			if optional.Err() != nil {
				goto done
			}
			continue
		case image := <-reads:
			stopRead()
			if image.err != nil {
				manifest.Warnings = append(manifest.Warnings, "图片未导入："+reference)
				continue
			}
			identifier := imported[image.file.Hash]
			if identifier == "" {
				imageCtx, stop := context.WithTimeout(optional, 5*time.Second)
				result, importErr := c.ImportInput(imageCtx, image.file.Name, image.file.MIMEType, image.file.Hash, image.file.Base64, int64(image.file.Size))
				stop()
				if importErr != nil {
					manifest.Warnings = append(manifest.Warnings, "图片未导入："+reference)
					continue
				}
				identifier = result.Resource.ResourceID
				if !markdownImageResourceID.MatchString(identifier) {
					manifest.Warnings = append(manifest.Warnings, "图片未导入："+reference)
					continue
				}
				imported[image.file.Hash] = identifier
			}
			manifest.Images = append(manifest.Images, markdownDependency{reference, identifier})
		}
	}
done:
	metadata, err := json.Marshal(manifest)
	if err != nil || len(metadata) > 48<<10 {
		return file
	}
	snapshot := append(append([]byte(nil), raw...), []byte("\n\n<!-- lazymind-markdown-images:v1:"+base64.StdEncoding.EncodeToString(metadata)+" -->\n")...)
	if len(snapshot) > maxLocalArtifactBytes {
		return file
	}
	sum := sha256.Sum256(snapshot)
	file.Base64 = base64.StdEncoding.EncodeToString(snapshot)
	file.Size = len(snapshot)
	file.Hash = "sha256:" + hex.EncodeToString(sum[:])
	return file
}
