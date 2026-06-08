package feishu

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

type exportTarget struct {
	kind      ObjectKind
	token     string
	spaceID   string
	driveType string
	objectKey string
}

func (c *FeishuConnector) locateObject(req connector.ExportObjectRequest) (exportTarget, error) {
	if req.SourceVersion == "" {
		return exportTarget{}, connector.NewError(connector.ErrorCodeInvalidArgument, "source_version is required")
	}
	kind := ObjectKind(req.ProviderMeta["kind"])
	token := req.ProviderMeta["token"]
	if token == "" {
		token = req.ObjectKey
	}
	target := exportTarget{
		kind:      kind,
		token:     token,
		spaceID:   req.ProviderMeta["space_id"],
		driveType: strings.ToLower(strings.TrimSpace(req.ProviderMeta["file_type"])),
		objectKey: req.ObjectKey,
	}
	switch kind {
	case ObjectKindDriveFile:
		if req.ExportFormat != "" && req.ExportFormat != connector.ExportFormatOriginal {
			return exportTarget{}, connector.NewError(connector.ErrorCodeUnsupported, "drive file export_format is not supported")
		}
	case ObjectKindWikiNode:
		if req.ExportFormat != "" && req.ExportFormat != connector.ExportFormatOriginal && req.ExportFormat != connector.ExportFormatMarkdown {
			return exportTarget{}, connector.NewError(connector.ErrorCodeUnsupported, "wiki export_format is not supported")
		}
		if target.spaceID == "" {
			return exportTarget{}, connector.NewError(connector.ErrorCodeInvalidArgument, "space_id is required")
		}
	default:
		return exportTarget{}, connector.NewError(connector.ErrorCodeInvalidArgument, "object is not exportable")
	}
	if target.token == "" {
		return exportTarget{}, connector.NewError(connector.ErrorCodeInvalidArgument, "provider token is required")
	}
	return target, nil
}

func (c *FeishuConnector) exportToTempURI(ctx context.Context, token string, target exportTarget, req connector.ExportObjectRequest) (ExportedContent, error) {
	switch target.kind {
	case ObjectKindDriveFile:
		if target.driveType == "docx" || target.driveType == "doc" {
			return c.api.ExportDriveDocumentMarkdown(ctx, token, target.token, req.SourceVersion)
		}
		return c.api.DownloadDriveFile(ctx, token, target.token, req.SourceVersion)
	case ObjectKindWikiNode:
		return c.api.ExportWikiNodeMarkdown(ctx, token, target.spaceID, target.token, req.SourceVersion)
	default:
		return ExportedContent{}, connector.NewError(connector.ErrorCodeInvalidArgument, "object is not exportable")
	}
}

func (c *FeishuConnector) verifyExportedVersion(exported ExportedContent, sourceVersion string) error {
	if exported.ContentURI == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri is required")
	}
	if !strings.HasPrefix(exported.ContentURI, "scan-temp://") {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "content_uri must use scan-temp scheme")
	}
	if exported.ExportedVersion != sourceVersion {
		return connector.NewError(connector.ErrorCodeVersionMismatch, "exported version does not match requested source_version")
	}
	return nil
}

func (c *FeishuConnector) ensureScanTempURI(ctx context.Context, exported ExportedContent) (ExportedContent, error) {
	if strings.HasPrefix(strings.TrimSpace(exported.ContentURI), "scan-temp://") {
		return exported, nil
	}
	reader := exported.Reader
	if reader == nil && exported.Content != nil {
		reader = bytes.NewReader(exported.Content)
	}
	if reader == nil {
		return exported, nil
	}
	if c.temp == nil {
		return ExportedContent{}, connector.NewError(connector.ErrorCodeInvalidArgument, "temp object store is required for feishu export")
	}
	temp, err := c.temp.Put(ctx, worker.TempObjectInput{Reader: reader})
	if err != nil {
		return ExportedContent{}, err
	}
	if closer, ok := reader.(io.Closer); ok {
		_ = closer.Close()
	}
	exported.ContentURI = temp.URI
	exported.CleanupToken = temp.CleanupToken
	if exported.SizeBytes == 0 {
		exported.SizeBytes = temp.SizeBytes
	}
	return exported, nil
}

func (c *FeishuConnector) buildExportedObject(exported ExportedContent) connector.ExportedObject {
	return connector.ExportedObject{
		ContentURI:      exported.ContentURI,
		MimeType:        exported.MimeType,
		FileExtension:   exported.FileExtension,
		SizeBytes:       exported.SizeBytes,
		CleanupToken:    exported.CleanupToken,
		ExportedVersion: exported.ExportedVersion,
	}
}
