package mcpbridge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
)

func (b *Bridge) prepareOriginalArguments(ctx context.Context, request *mcp.CallToolRequest, arguments any) (any, error) {
	if request.Params.Name != "knowledge.document.upload" {
		return arguments, nil
	}
	values, ok := arguments.(map[string]any)
	if !ok {
		return nil, errors.New("upload arguments must be an object")
	}
	files, _ := values["files"].([]any)
	hasLocal := false
	for _, item := range files {
		file, _ := item.(map[string]any)
		source, _ := file["source"].(map[string]any)
		hasLocal = hasLocal || source["kind"] == "local_file"
	}
	if !hasLocal {
		return arguments, nil
	}
	if request.Session == nil || request.Session.InitializeParams() == nil || request.Session.InitializeParams().Capabilities.RootsV2 == nil {
		return nil, errors.New("LOCAL_FILE_BRIDGE_REQUIRED: host must advertise authorized filesystem roots before local upload")
	}
	listed, err := request.Session.ListRoots(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("read host-authorized roots: %w", err)
	}
	roots := []string{}
	for _, root := range listed.Roots {
		if root == nil {
			continue
		}
		u, err := url.Parse(root.URI)
		if err != nil || u.Scheme != "file" {
			continue
		}
		path := u.Path
		if runtime.GOOS == "windows" {
			if u.Host != "" && u.Host != "localhost" {
				path = "//" + u.Host + path
			} else if len(path) > 2 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
		} else if u.Host != "" && u.Host != "localhost" {
			continue
		}
		roots = append(roots, filepath.FromSlash(path))
	}
	if len(roots) == 0 {
		return nil, errors.New("LOCAL_FILE_BRIDGE_REQUIRED: no authorized filesystem roots were supplied by the host")
	}
	return transferLocalOriginals(ctx, values, roots, b.api.UploadOriginal)
}
func transferLocalOriginals(ctx context.Context, arguments map[string]any, roots []string, upload func(context.Context, *os.File, string) (string, error)) (map[string]any, error) {
	files, ok := arguments["files"].([]any)
	if !ok {
		return nil, errors.New("files array is required")
	}
	type pendingFile struct {
		file *os.File
		name string
		item map[string]any
	}
	pending := []pendingFile{}
	defer func() {
		for _, p := range pending {
			p.file.Close()
		}
	}()
	result := make(map[string]any, len(arguments))
	for k, v := range arguments {
		result[k] = v
	}
	out := append([]any(nil), files...)
	result["files"] = out
	// Authorize every local file before sending any bytes.
	for index, item := range files {
		original, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("upload item must be an object")
		}
		source, _ := original["source"].(map[string]any)
		if source["kind"] != "local_file" {
			continue
		}
		raw, _ := source["path"].(string)
		file, err := openAuthorizedOriginal(raw, roots)
		if err != nil {
			return nil, err
		}
		next := make(map[string]any, len(original))
		for k, v := range original {
			next[k] = v
		}
		out[index] = next
		pending = append(pending, pendingFile{file: file, name: filepath.Base(raw), item: next})
	}
	for _, p := range pending {
		ref, err := upload(ctx, p.file, p.name)
		if err != nil {
			return nil, err
		}
		p.item["source"] = map[string]any{"kind": "uploaded_file", "file_ref": ref}
	}
	return result, nil
}
func openAuthorizedOriginal(path string, roots []string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("local file path must be absolute on the executing host")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		// os.Root enforces traversal safety even if a symlink changes after validation.
		directory, err := os.OpenRoot(root)
		if err != nil {
			continue
		}
		file, err := directory.Open(relative)
		directory.Close()
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > coreapi.MaxOriginalBytes {
			file.Close()
			return nil, errors.New("local original must be a regular file of 1 byte to 128 MiB")
		}
		return file, nil
	}
	return nil, errors.New("FORBIDDEN: local file is outside host-authorized filesystem roots")
}
