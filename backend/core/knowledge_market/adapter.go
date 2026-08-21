package knowledge_market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/knowledge_market/download"
	"lazymind/core/knowledge_market/source"
)

// materializeMarketFiles converts a downloaded package into document import
// records. When the catalog item has a source adapter, that adapter selects and
// materializes only the knowledge files; otherwise the legacy behavior imports
// every downloaded file.
func materializeMarketFiles(ctx context.Context, item orm.KnowledgeMarketItem, files []download.FetchedFile, root string) ([]doc.MarketImportFile, error) {
	adapterID := strings.TrimSpace(item.SourceAdapter)
	if adapterID == "" {
		return legacyMarketImportFiles(files, root), nil
	}

	options, err := decodeSourceOptions(item.SourceOptions)
	if err != nil {
		return nil, fmt.Errorf("decode source options: %w", err)
	}
	adapter, err := source.New(adapterID, options)
	if err != nil {
		return nil, err
	}
	if adapter == nil {
		return legacyMarketImportFiles(files, root), nil
	}

	entries := make([]source.FileEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, source.FileEntry{
			Path:   file.Path,
			Size:   file.Size,
			SHA256: file.SHA256,
		})
	}
	selected := source.Filter(entries, adapter)
	units, err := adapter.Materialize(ctx, root, selected)
	if err != nil {
		return nil, err
	}

	out := make([]doc.MarketImportFile, 0, len(units))
	for _, unit := range units {
		out = append(out, doc.MarketImportFile{
			LocalPath:    unit.LocalPath,
			DisplayName:  unit.DisplayName,
			RelativePath: unit.RelativePath,
			Tags:         unit.Tags,
		})
	}
	return out, nil
}

func decodeSourceOptions(raw json.RawMessage) (source.Options, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return source.Options{}, nil
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return source.Options(values), nil
}

func legacyMarketImportFiles(files []download.FetchedFile, root string) []doc.MarketImportFile {
	out := make([]doc.MarketImportFile, 0, len(files))
	for _, file := range files {
		out = append(out, doc.MarketImportFile{
			LocalPath:    filepath.Join(root, filepath.FromSlash(file.Path)),
			DisplayName:  filepath.Base(file.Path),
			RelativePath: filepath.Dir(file.Path),
		})
	}
	return out
}
