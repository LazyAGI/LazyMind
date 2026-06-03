package localfs

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
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
	return cleanPath(path), agentID, nil
}

func (c *LocalFSConnector) exportToTempURI(ctx context.Context, agentID, path, expectedVersion string) (ExportedFile, error) {
	return c.agent.ExportFile(ctx, ExportFileRequest{
		AgentID:         agentID,
		Path:            path,
		ExpectedVersion: expectedVersion,
	})
}

func (c *LocalFSConnector) verifyExportedVersion(exported ExportedFile, sourceVersion string) error {
	if version := exportedVersion(exported); version != sourceVersion {
		return connector.NewError(connector.ErrorCodeVersionMismatch, "exported version does not match requested source_version")
	}
	if exported.ContentURI == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri is required")
	}
	return nil
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
