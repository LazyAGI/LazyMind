package documentdirectory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

const organizerInputBytes = 64 * 1024
const organizerChunkSize = 30

func entryContext(e orm.DocumentDirectoryEntry) map[string]any {
	return map[string]any{"resource_key": e.ResourceKey, "title": e.Title, "group_id": e.GroupID, "pinned": e.Pinned, "confirmed": e.Confirmed, "read_state": e.ReadState, "content_version": e.ContentVersion}
}

func organizationContext(d orm.DocumentDirectory, groups []orm.DocumentDirectoryGroup, entries []orm.DocumentDirectoryEntry, input Input, examples bool) map[string]any {
	resources := organizerResources(input, entries)
	groupContext := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		groupContext = append(groupContext, map[string]any{"id": g.ID, "name": g.Name, "parent_id": g.ParentID, "position": g.Position})
	}
	related := []map[string]any{}
	samples := []map[string]any{}
	seen := map[string]bool{}
	// SQL callers provide stable group/id order, so examples do not vary with map iteration.
	for _, e := range entries {
		if e.Excluded {
			continue
		}
		if _, ok := resources[e.ResourceKey]; ok {
			related = append(related, entryContext(e))
			continue
		}
		if examples && e.Confirmed && !seen[e.GroupID] && len(samples) < 20 {
			samples = append(samples, entryContext(e))
			seen[e.GroupID] = true
		}
	}
	return map[string]any{"directory": map[string]any{"id": d.ID, "name": d.Name, "intent": d.Intent, "organization": d.Organization}, "groups": groupContext, "entries": append(related, samples...), "resources": resources, "instruction": input.Instruction}
}

// Preserve the original observation IDs and evidence payloads, selecting only
// resources in this chunk; historical bodies never become model context.
func selectResources(input Input, keys []string) Input {
	selected := map[string]bool{}
	for _, k := range keys {
		selected[k] = true
	}
	chunk := input
	chunk.ResourceKeys = nil
	chunk.Entries = nil
	chunk.Observations = nil
	for _, e := range input.Entries {
		if selected[e.ResourceKey] {
			chunk.Entries = append(chunk.Entries, e)
		}
	}
	for _, row := range input.Observations {
		var o Observation
		if json.Unmarshal(row.PayloadJSON, &o) != nil {
			continue
		}
		candidates := []Candidate{}
		for _, c := range o.Candidates {
			if selected[resourceKey(c)] {
				candidates = append(candidates, c)
			}
		}
		if len(candidates) > 0 {
			o.Candidates = candidates
			row.PayloadJSON = encoded(o)
			chunk.Observations = append(chunk.Observations, row)
		}
	}
	return chunk
}

func boundedOrganizationChunk(d orm.DocumentDirectory, groups []orm.DocumentDirectoryGroup, entries []orm.DocumentDirectoryEntry, input Input, keys []string) (Input, map[string]any, int, error) {
	for n := min(organizerChunkSize, len(keys)); n > 0; n-- {
		chunk := selectResources(input, keys[:n])
		for _, examples := range []bool{true, false} {
			payload := organizationContext(d, groups, entries, chunk, examples)
			if len(encoded(payload)) <= organizerInputBytes {
				return chunk, payload, n, nil
			}
		}
	}
	return Input{}, nil, 0, fmt.Errorf("%w: directory_model_input_too_large", ErrInput)
}

func organizeChunks(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, input Input, generate func(string, any) (Proposal, error)) error {
	// Retry may receive an older caller snapshot; the durable order wins.
	var saved Input
	if err := json.Unmarshal(b.InputJSON, &saved); err != nil {
		return err
	}
	input.ResourceKeys = saved.ResourceKeys
	source := input
	if input.FolderInventoryID != "" {
		v, err := inventory(db, b.UserID, input.FolderInventoryID)
		if err != nil {
			return err
		}
		if err = db.Where("directory_id=?", b.DirectoryID).Order("id").Find(&source.Entries).Error; err != nil {
			return err
		}
		if err = db.Where("user_id=? AND batch_id IN ?", b.UserID, []string{b.ID, v.BatchID}).Order("created_at,id").Find(&source.Observations).Error; err != nil {
			return err
		}
		source.Instruction += "\nAll resources already belong to this folder. Classify only; reuse groups and leave uncertain items unclassified."
	}
	// Build the evidence lookup once per execution, keeping separate observations
	// for each resource instead of repeatedly scanning the full folder history.
	evidence := map[string][]orm.DocumentObservation{}
	for _, row := range source.Observations {
		var o Observation
		if err := json.Unmarshal(row.PayloadJSON, &o); err != nil {
			return err
		}
		grouped := map[string][]Candidate{}
		for _, c := range o.Candidates {
			k := resourceKey(c)
			grouped[k] = append(grouped[k], c)
		}
		for k, items := range grouped {
			part := o
			part.Candidates = items
			copy := row
			copy.PayloadJSON = encoded(part)
			evidence[k] = append(evidence[k], copy)
		}
	}
	entryIndex := map[string]orm.DocumentDirectoryEntry{}
	for _, e := range source.Entries {
		entryIndex[e.ResourceKey] = e
	}
	if input.ResourceKeys == nil {
		input.ResourceKeys = []string{}
		if input.FolderInventoryID != "" {
			// Legacy checkpoints count entry slots in ID order, including excluded
			// entries. Preserve that order when freezing a pre-upgrade retry.
			for _, e := range source.Entries {
				input.ResourceKeys = append(input.ResourceKeys, e.ResourceKey)
			}
		} else {
			for k := range candidates(source) {
				input.ResourceKeys = append(input.ResourceKeys, k)
			}
			sort.Strings(input.ResourceKeys)
		}
		if err := mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
			if err := ownsJob(tx, j); err != nil {
				return err
			}
			b.InputJSON = encoded(input)
			return tx.Model(b).Update("input_json", b.InputJSON).Error
		}); err != nil {
			return err
		}
	}
	for {
		var progress struct {
			Processed int `json:"processed"`
		}
		if err := json.Unmarshal(b.ResultJSON, &progress); err != nil && len(b.ResultJSON) > 0 {
			return err
		}
		if progress.Processed < 0 || progress.Processed > len(input.ResourceKeys) {
			return ErrInput
		}
		if progress.Processed == len(input.ResourceKeys) {
			return finishOrganization(ctx, db, j, b, input)
		}
		var d orm.DocumentDirectory
		var groups []orm.DocumentDirectoryGroup
		var entries []orm.DocumentDirectoryEntry
		if err := mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
			if err := ownsJob(tx, j); err != nil {
				return err
			}
			var err error
			d, err = requireDirectory(tx, b.UserID, b.DirectoryID)
			if err != nil {
				return err
			}
			if !input.Manual && !d.AutoAdd {
				return ErrInput
			}
			if err = tx.Where("directory_id=?", d.ID).Order("position,id").Find(&groups).Error; err != nil {
				return err
			}
			return tx.Where("directory_id=?", d.ID).Order("group_id,id").Find(&entries).Error
		}); err != nil {
			return err
		}
		// Bound the selection before constructing the payload; the per-resource index
		// prevents repeated full observation scans across chunks.
		keys := input.ResourceKeys[progress.Processed:min(progress.Processed+organizerChunkSize, len(input.ResourceKeys))]
		work := source
		work.Entries = nil
		work.Observations = nil
		for _, k := range keys {
			if e, ok := entryIndex[k]; ok {
				work.Entries = append(work.Entries, e)
			}
			work.Observations = append(work.Observations, evidence[k]...)
		}
		chunk, payload, n, err := boundedOrganizationChunk(d, groups, entries, work, keys)
		if err != nil {
			return err
		}
		p := Proposal{Assignments: []Assignment{}}
		if len(payload["resources"].(map[string]Candidate)) > 0 {
			p, err = generate("organize", payload)
			if err != nil {
				return err
			}
		}
		chunk.Chunked = true
		chunk.ChunkOffset = progress.Processed + n
		if err = applyOrganization(ctx, db, j, b, d.Version, chunk, p); err != nil {
			return err
		}
	}
}

func finishOrganization(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, input Input) error {
	return mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
		if err := ownsJob(tx, j); err != nil {
			return err
		}
		d, err := requireDirectory(tx, b.UserID, b.DirectoryID)
		if err != nil {
			return err
		}
		grouped, unclassified, err := classificationCounts(tx, d.ID)
		if err != nil {
			return err
		}
		evicted := ""
		if !d.Ready {
			if !input.Reorganize && input.FolderInventoryID == "" && grouped+unclassified == 0 {
				if err = tx.Delete(&d).Error; err != nil {
					return err
				}
				b.Acknowledged = true
			} else {
				evicted, err = makeRoom(tx, b.UserID, d.ID, input.SourceDirectoryID)
				if err != nil {
					return err
				}
				if err = tx.Model(&d).Updates(map[string]any{"ready": true, "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
			}
		}
		b.Status = "succeeded"
		b.UpdatedAt = time.Now().UTC()
		b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"classification_state": "complete", "grouped": grouped, "unclassified": unclassified, "directory_name": d.Name, "evicted": evicted})
		if err = tx.Save(b).Error; err != nil {
			return err
		}
		if b.Acknowledged {
			return acknowledgeParents(tx, b.UserID, b.ParentID)
		}
		return nil
	})
}

// Retain every attempt separately and sum numeric usage, including failed calls
// that reported consumption. Never replace earlier chunks with the last call.
func modelUsage(attempts []any) map[string]any {
	total := map[string]any{}
	var add func(map[string]any, map[string]any)
	add = func(dst, src map[string]any) {
		for key, value := range src {
			switch n := value.(type) {
			case float64:
				old, _ := dst[key].(float64)
				dst[key] = old + n
			case int:
				old, _ := dst[key].(float64)
				dst[key] = old + float64(n)
			case int64:
				old, _ := dst[key].(float64)
				dst[key] = old + float64(n)
			case bool:
				old, _ := dst[key].(bool)
				dst[key] = old || n
			case map[string]any:
				nested, _ := dst[key].(map[string]any)
				if nested == nil {
					nested = map[string]any{}
				}
				add(nested, n)
				dst[key] = nested
			}
		}
	}
	for _, attempt := range attempts {
		if item, ok := attempt.(map[string]any); ok {
			if usage, ok := item["usage"].(map[string]any); ok {
				add(total, usage)
			}
		}
	}
	return total
}

func routeChunks(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, input Input, generate func(string, any) (Proposal, error)) error {
	keys := []string{}
	for k := range candidates(input) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var progress struct {
		Processed int      `json:"route_processed"`
		Targets   []Target `json:"route_targets"`
	}
	if len(b.ResultJSON) > 0 {
		if err := json.Unmarshal(b.ResultJSON, &progress); err != nil {
			return err
		}
	}
	if progress.Processed < 0 || progress.Processed > len(keys) {
		return ErrInput
	}
	for progress.Processed < len(keys) {
		var dirs []orm.DocumentDirectory
		if err := db.WithContext(ctx).Where("user_id=? AND deleted_at IS NULL", b.UserID).Order("id").Find(&dirs).Error; err != nil {
			return err
		}
		n := min(organizerChunkSize, len(keys)-progress.Processed)
		var payload map[string]any
		for ; n > 0; n-- {
			payload = routeInput(selectResources(input, keys[progress.Processed:progress.Processed+n]), dirs)
			if len(encoded(payload)) <= organizerInputBytes {
				break
			}
		}
		if n == 0 {
			return fmt.Errorf("%w: directory_model_input_too_large", ErrInput)
		}
		p, err := generate("route", payload)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, k := range keys[progress.Processed : progress.Processed+n] {
			allowed[k] = true
		}
		for _, target := range p.Targets {
			for _, k := range target.ResourceKeys {
				if !allowed[k] {
					return ErrInput
				}
			}
			found := -1
			for i, prior := range progress.Targets {
				if target.DirectoryID != "" && prior.DirectoryID == target.DirectoryID || target.DirectoryID == "" && prior.DirectoryID == "" && prior.Intent == target.Intent && prior.Organization == target.Organization {
					found = i
					break
				}
			}
			if found < 0 {
				progress.Targets = append(progress.Targets, target)
			} else {
				progress.Targets[found].ResourceKeys = append(progress.Targets[found].ResourceKeys, target.ResourceKeys...)
			}
		}
		progress.Processed += n
		if err = mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
			if err := ownsJob(tx, j); err != nil {
				return err
			}
			b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"route_processed": progress.Processed, "route_targets": progress.Targets})
			return tx.Model(b).Update("result_json", b.ResultJSON).Error
		}); err != nil {
			return err
		}
	}
	return applyTargets(ctx, db, j, b, input, Proposal{Targets: progress.Targets})
}
