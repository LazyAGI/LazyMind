package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"lazymind/core/doc"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

type imageExportSource struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Source       string   `json:"source,omitempty"`
	Local        bool     `json:"local,omitempty"`
	References   []string `json:"references,omitempty"`
	ExpectedHash string   `json:"expected_hash,omitempty"`
}

type imageLibraryAsset struct {
	ID   string `json:"media_asset_id"`
	Path string `json:"local_path"`
	URI  string `json:"uri"`
	Meta struct {
		Source string `json:"source_reference"`
		SHA256 string `json:"sha256"`
	} `json:"meta"`
}

func imageReferenceKey(source string) string {
	// Only our internal static references have disposable URL signatures.
	if u, err := url.Parse(source); err == nil {
		if strings.HasPrefix(u.Path, "/static-files/") {
			return u.Path
		}
		if u.Scheme == "" && u.Host == "" && u.Path != "" {
			return path.Clean(u.Path)
		}
	}
	return source
}

func documentImageSources(ctx context.Context, target *documentActionContext, refs []imageReference) ([]imageExportSource, error) {
	artifacts, err := workflowstore.New(target.db).ListArtifacts(ctx, target.owner, target.session.ID)
	if err != nil {
		return nil, err
	}
	slots := []string{"resolved_media_assets", "media_assets"}
	if target.revision.SlotID == "flat_draft_document" {
		slots[0] = "flat_resolved_media_assets"
	}
	if target.revision.SlotID == "source_document" || target.revision.SlotID == "outline_document" {
		slots = []string{"media_assets"}
	}
	library := map[string]imageExportSource{}
	add := func(key string, asset imageExportSource) {
		key = imageReferenceKey(key)
		if key != "" {
			if _, exists := library[key]; !exists {
				library[key] = asset
			}
		}
	}
	for _, slot := range slots {
		for _, artifact := range artifacts {
			if artifact.SlotID != slot || artifact.Validity != "effective" {
				continue
			}
			// File/JSON describes the carrier, not the media library's schema.
			// Read its declared schema from the server-owned artifact itself.
			raw, readErr := document.ReadArtifactValue(artifact.Value)
			if readErr != nil {
				continue
			}
			var envelope struct {
				Assets json.RawMessage `json:"assets"`
			}
			if json.Unmarshal(document.ArtifactData(raw), &envelope) != nil {
				continue
			}
			assets := map[string]imageLibraryAsset{}
			if json.Unmarshal(envelope.Assets, &assets) != nil {
				var items []imageLibraryAsset
				if json.Unmarshal(envelope.Assets, &items) != nil {
					continue
				}
				for _, asset := range items {
					if asset.ID != "" {
						assets[asset.ID] = asset
					}
				}
			}
			for id, asset := range assets {
				source := asset.Path
				if source == "" {
					source = asset.URI
				}
				item := imageExportSource{Source: source, Local: filepath.IsAbs(source) || strings.HasPrefix(source, "/static-files/"), ExpectedHash: strings.ToLower(asset.Meta.SHA256)}
				for _, alias := range []string{"asset://" + id, asset.Path, asset.URI, asset.Meta.Source} {
					add(alias, item)
				}
				if item.Local {
					add(doc.StaticFileReferenceFromAnyStoragePath(source), item)
				}
				if asset.ID != "" {
					add("asset://"+asset.ID, item)
				}
				if len(item.ExpectedHash) == 64 {
					ext := filepath.Ext(source)
					if u, e := url.Parse(source); e == nil {
						ext = filepath.Ext(u.Path)
					}
					if ext != "" {
						alias := "assets/" + item.ExpectedHash[:2] + "/" + item.ExpectedHash + ext
						add(alias, item)
						add("_"+alias, item)
					}
				}
			}
		}
	}
	for _, artifact := range artifacts {
		if artifact.SlotID != "target_document" || artifact.Validity != "effective" {
			continue
		}
		raw, e := document.ReadArtifactValue(artifact.Value, artifact.ContentType)
		if e != nil {
			continue
		}
		var target struct {
			Meta struct {
				Aliases map[string]string `json:"github_writer_media_aliases"`
			} `json:"meta"`
		}
		if json.Unmarshal(document.ArtifactData(raw), &target) == nil {
			for alias, id := range target.Meta.Aliases {
				if asset, ok := library["asset://"+id]; ok {
					add(alias, asset)
				}
			}
		}
	}
	items := []imageExportSource{}
	bySource := map[string]int{}
	for _, ref := range refs {
		item, found := library[imageReferenceKey(ref.Source)]
		if !found && ref.Fallback != "" {
			item, found = library[imageReferenceKey(ref.Fallback)]
		}
		if !found {
			item.Source = ref.Source
			if strings.HasPrefix(item.Source, "asset://") && ref.Fallback != "" {
				item.Source = ref.Fallback
			}
			// Inline editor paths are not trusted filesystem capabilities. Only
			// the current user's uploaded images can be introduced without a library entry.
			if doc.TempUserUploadOwnedBy(item.Source, target.owner) {
				item.Local = true
			} else if !strings.HasPrefix(item.Source, "https://") && !strings.HasPrefix(item.Source, "http://") && !strings.HasPrefix(item.Source, "data:image/") {
				item.Source = ""
			}
		}
		identity := imageReferenceKey(item.Source)
		if identity != "" {
			if index, ok := bySource[identity]; ok {
				items[index].References = append(items[index].References, ref.Source)
				continue
			}
		}
		item.ID = fmt.Sprintf("image-%03d", len(items)+1)
		item.Name = imageExportName(ref.Alt)
		if item.Name == "" {
			item.Name = "image"
		}
		item.References = []string{ref.Source}
		bySource[identity] = len(items)
		items = append(items, item)
	}
	return items, nil
}

func imageExportHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
