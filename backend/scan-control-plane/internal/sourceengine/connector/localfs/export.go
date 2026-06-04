package localfs

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

func (c *LocalFSConnector) locateObject(req connector.ExportObjectRequest) (string, string, error) {
	if req.SourceVersion == "" {
		return "", "", connector.NewError(connector.ErrorCodeInvalidArgument, "source_version is required")
	}
	if req.ExportFormat != "" && req.ExportFormat != connector.ExportFormatOriginal {
		return "", "", connector.NewError(connector.ErrorCodeUnsupported, "export_format is not supported")
	}
	agentID := strings.TrimSpace(req.ProviderMeta["agent_id"])
	path := strings.TrimSpace(req.ProviderMeta["path"])
	if path == "" {
		if decoded, ok := pathFromObjectKey(req.ObjectKey); ok {
			path = decoded
		}
	}
	if agentID == "" || path == "" {
		return "", "", connector.NewError(connector.ErrorCodeInvalidArgument, "agent_id and path provider meta are required")
	}
	if c.agent == nil {
		return "", "", connector.NewError(ErrorCodeAgentNotAvailable, "agent client is not configured")
	}
	path = cleanPath(path)
	if err := c.rejectOutsidePublicRoot(path); err != nil {
		return "", "", err
	}
	return path, agentID, nil
}

func (c *LocalFSConnector) exportToTempURI(ctx context.Context, agentID, path, expectedVersion string) (ExportedFile, error) {
	exported, err := c.agent.ExportFile(ctx, ExportFileRequest{
		AgentID:         agentID,
		Path:            path,
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		return ExportedFile{}, err
	}
	return c.ensureScanTempURI(ctx, exported)
}

func (c *LocalFSConnector) verifyExportedVersion(exported ExportedFile, sourceVersion string) error {
	if version := exportedVersion(exported); version != sourceVersion {
		return connector.NewError(connector.ErrorCodeVersionMismatch, "exported version does not match requested source_version")
	}
	if exported.ContentURI == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri is required")
	}
	if !strings.HasPrefix(strings.TrimSpace(exported.ContentURI), "scan-temp://") {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri must use scan-temp scheme")
	}
	return nil
}

func (c *LocalFSConnector) ensureScanTempURI(ctx context.Context, exported ExportedFile) (ExportedFile, error) {
	if strings.HasPrefix(strings.TrimSpace(exported.ContentURI), "scan-temp://") {
		return exported, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(exported.ContentURI), "file://") {
		return ExportedFile{}, connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri must use file or scan-temp scheme")
	}
	if c.temp == nil {
		return ExportedFile{}, connector.NewError(connector.ErrorCodeInvalidArgument, "temp object store is required for local_fs export")
	}
	path, err := fileURIPath(exported.ContentURI)
	if err != nil {
		return ExportedFile{}, connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri is not a valid file uri")
	}
	file, err := os.Open(path)
	if err != nil {
		return ExportedFile{}, err
	}
	defer file.Close()
	temp, err := c.temp.Put(ctx, worker.TempObjectInput{Reader: file})
	if err != nil {
		return ExportedFile{}, err
	}
	exported.ContentURI = temp.URI
	exported.CleanupToken = temp.CleanupToken
	if exported.SizeBytes == 0 {
		exported.SizeBytes = temp.SizeBytes
	}
	return exported, nil
}

func fileURIPath(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" || parsed.Host != "" || parsed.Path == "" {
		return "", connector.NewError(connector.ErrorCodeInvalidArgument, "file uri must be local")
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (c *LocalFSConnector) buildExportedObject(exported ExportedFile) connector.ExportedObject {
	return connector.ExportedObject{
		ContentURI:      exported.ContentURI,
		MimeType:        exported.MimeType,
		FileExtension:   exported.FileExtension,
		SizeBytes:       exported.SizeBytes,
		CleanupToken:    exported.CleanupToken,
		ExportedVersion: exportedVersion(exported),
	}
}

func exportedVersion(exported ExportedFile) string {
	return versionFor(PathInfo{MTimeUnixNano: exported.MTimeUnixNano, SizeBytes: exported.SizeBytes})
}
