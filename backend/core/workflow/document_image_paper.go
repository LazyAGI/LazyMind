package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"lazymind/core/doc"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

// Paper publication addresses SVGs by their content hash, while F4 registers
// them as analysis_chart artifacts rather than a Writer media library. Bind
// only those hashes to the current session's authorized, effective originals.
func addPaperChartImageSources(ctx context.Context, target *documentActionContext, artifacts []workflowstore.Artifact, add func(string, imageExportSource)) {
	if target.session.WorkflowID != "paper-writing" {
		return
	}
	for _, artifact := range artifacts {
		if artifact.SlotID != "analysis_chart" || artifact.Validity != "effective" || !artifact.Selected {
			continue
		}
		var carrier struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}
		if json.Unmarshal(document.ArtifactData(artifact.Value), &carrier) != nil || strings.ToLower(filepath.Ext(carrier.Path)) != ".svg" {
			continue
		}
		data, err := doc.ReadDocumentImage(ctx, carrier.Path, target.owner, maxExportImageBytes)
		if err != nil || imageFileExtension(data) != ".svg" {
			continue
		}
		digest := imageExportHash(data)
		if carrier.SHA256 != "" && !strings.EqualFold(strings.TrimPrefix(carrier.SHA256, "sha256:"), digest) {
			continue
		}
		add("/static-files/paper-charts/"+digest+".svg", imageExportSource{
			Source: carrier.Path, Local: true, ExpectedHash: digest,
		})
	}
}
