package workflowmcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lazymind/agentconnector/internal/connectorconfig"
)

// Input paths are explicit Connector-host paths. A process cwd is neither a
// conversation workspace nor an authorization root. Managed installations may
// restrict input reads with connection-owned roots, never model-supplied roots.
func openLocalInputFile(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("local_path must be an absolute path on the Connector machine; resolve or download the attachment first")
	}
	config, err := connectorconfig.Current()
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve local_path: %w", err)
	}
	// Reject directories/devices before opening, including named pipes whose
	// open could otherwise block. Recheck the opened handle before reading.
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if err := validateInputFile(info); err != nil {
		return nil, err
	}
	var file *os.File
	if config == nil || len(config.WorkflowInputRoots) == 0 {
		file, err = os.Open(resolved)
	} else {
		for _, configured := range config.WorkflowInputRoots {
			rootPath, resolveErr := filepath.EvalSymlinks(configured)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve workflow_input_roots: %w", resolveErr)
			}
			relative, relErr := filepath.Rel(rootPath, resolved)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			root, openErr := os.OpenRoot(rootPath)
			if openErr != nil {
				return nil, fmt.Errorf("open workflow_input_roots: %w", openErr)
			}
			file, err = root.Open(relative)
			_ = root.Close()
			break
		}
		if file == nil && err == nil {
			return nil, errors.New("local_path is outside configured workflow_input_roots")
		}
	}
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err == nil {
		err = validateInputFile(info)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func validateInputFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("local_path must be a regular file")
	}
	if info.Size() > maxLocalArtifactBytes {
		return errors.New("local file exceeds 20 MiB")
	}
	return nil
}

func readLocalInputFile(path string) (localFile, error) {
	file, err := openLocalInputFile(path)
	if err != nil {
		return localFile{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLocalArtifactBytes+1))
	if err != nil {
		return localFile{}, err
	}
	if len(data) > maxLocalArtifactBytes {
		return localFile{}, errors.New("local file exceeds 20 MiB")
	}
	name := filepath.Base(path)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	sum := sha256.Sum256(data)
	return localFile{Name: name, MIMEType: contentType, Size: len(data), Hash: "sha256:" + hex.EncodeToString(sum[:]),
		Base64: base64.StdEncoding.EncodeToString(data)}, nil
}
