package notion

import (
	"bytes"
	"context"
	"io"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

func (c *NotionConnector) buildExportedObject(ctx context.Context, exported ExportedContent) (connector.ExportedObject, error) {
	reader := exported.Reader
	if reader == nil && exported.Content != nil {
		reader = bytes.NewReader(exported.Content)
	}
	if reader == nil {
		return connector.ExportedObject{}, connector.NewError(connector.ErrorCodeInvalidArgument, "notion export content is empty")
	}
	if c.temp == nil {
		return connector.ExportedObject{}, connector.NewError(connector.ErrorCodeInvalidArgument, "temp object store is required for notion export")
	}
	temp, err := c.temp.Put(ctx, worker.TempObjectInput{Reader: reader})
	if err != nil {
		return connector.ExportedObject{}, err
	}
	if closer, ok := reader.(io.Closer); ok {
		_ = closer.Close()
	}
	if exported.SizeBytes == 0 {
		exported.SizeBytes = temp.SizeBytes
	}
	return connector.ExportedObject{
		ContentURI:      temp.URI,
		MimeType:        firstNonEmpty(exported.MimeType, "text/markdown"),
		FileExtension:   firstNonEmpty(exported.FileExtension, ".md"),
		SizeBytes:       exported.SizeBytes,
		CleanupToken:    temp.CleanupToken,
		ExportedVersion: exported.ExportedVersion,
	}, nil
}
