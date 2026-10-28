package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/learning"
	"lazymind/core/modelprovider"
	"lazymind/core/settings"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

func resourceScene(value string) (string, error) {
	scene, err := learning.NormalizeScene(value)
	if err != nil {
		return "", resourceErr("INVALID_ARGUMENT", err.Error())
	}
	return scene, nil
}

func knowledgeCapabilities(ctx context.Context, db *gorm.DB, user, scene, profile string, explicit []learning.CapabilityRef) ([]learning.CapabilityRef, error) {
	if profile != "" && scene != "custom" {
		return nil, resourceErr("INVALID_ARGUMENT", "profile_id requires custom scene")
	}
	var defaults []learning.CapabilityRef
	var err error
	if scene != "custom" || profile != "" {
		key := scene
		if profile != "" {
			key = profile
		}
		defaults, err = learning.New(db).ProfileRefs(ctx, user, key)
		if err != nil {
			return nil, resourceErr("INVALID_ARGUMENT", err.Error())
		}
	}
	if explicit != nil {
		defaults = explicit
	}
	if err := learning.ValidateCapabilityRefs(defaults); err != nil {
		return nil, resourceErr("INVALID_ARGUMENT", err.Error())
	}
	return defaults, nil
}

func knowledgeProcessingLevel(ctx context.Context, db *gorm.DB, user, requested string) (string, error) {
	controls, err := settings.LoadFeatureControls(ctx, db, user)
	if err != nil {
		return "", err
	}
	if requested == "" && !controls.DocumentParsingEnabled {
		return ProcessingLevelStored, nil
	}
	level, err := normalizeProcessingLevel(requested)
	if err != nil {
		return "", resourceErr("INVALID_ARGUMENT", err.Error())
	}
	if level != ProcessingLevelStored && !controls.DocumentParsingEnabled {
		return "", resourceErr("UNAVAILABLE", "document parsing is disabled")
	}
	if level == ProcessingLevelIndexed && systemdeps.PythonComponentActive("rag") {
		ready, checkErr := modelprovider.IsModelReady(ctx, db, user, "embed_main")
		if ready && checkErr == nil && modelprovider.GetCachedModelFeatures().ImageEmbedRequired {
			ready, checkErr = modelprovider.IsModelReady(ctx, db, user, "embed_image")
		}
		if requested == "" && (checkErr != nil || !ready) {
			return ProcessingLevelChunked, nil
		}
		if checkErr != nil || !ready {
			return "", resourceErr("UNAVAILABLE", "embedding model is not ready")
		}
	}
	return level, nil
}

func knowledgeAlgorithms(ctx context.Context) ([]Algo, error) {
	if !systemdeps.PythonComponentActive("rag") {
		return []Algo{}, nil
	}
	var response algoListResp
	if err := common.ApiGet(ctx, common.JoinURL(common.AlgoServiceEndpoint(), "/v1/algo/list"), nil, &response, 5*time.Second); err != nil {
		return nil, err
	}
	if response.Code != 200 {
		return nil, fmt.Errorf("algorithm catalog unavailable")
	}
	items := make([]Algo, 0, len(response.Data.Items))
	for _, item := range response.Data.Items {
		items = append(items, Algo{AlgoID: item.AlgoID, DisplayName: item.DisplayName, Description: item.Description})
	}
	return items, nil
}

func knowledgeAlgorithm(ctx context.Context, id string) (Algo, error) {
	if !systemdeps.PythonComponentActive("rag") {
		return Algo{AlgoID: strings.TrimSpace(id)}, nil
	}
	if strings.TrimSpace(id) == "" {
		return Algo{AlgoID: defaultMarketAlgoID, DisplayName: defaultMarketAlgoName}, nil
	}
	items, err := knowledgeAlgorithms(ctx)
	if err != nil {
		return Algo{}, resourceErr("UNAVAILABLE", err.Error())
	}
	for _, item := range items {
		if item.AlgoID == id {
			return item, nil
		}
	}
	return Algo{}, resourceErr("INVALID_ARGUMENT", "algorithm is not available")
}

func validateKnowledgeDescription(value string) error {
	if utf8.RuneCountInString(value) > 300 {
		return resourceErr("INVALID_ARGUMENT", "description exceeds 300 characters")
	}
	return nil
}

func knowledgeExt(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func updateKnowledgeConfiguration(ctx context.Context, tx *gorm.DB, user string, ds *orm.Dataset, c ResourceMetadataChanges, updates, before, after map[string]any) error {
	nextScene := ds.Scene
	if c.Scene != nil {
		var err error
		nextScene, err = resourceScene(*c.Scene)
		if err != nil {
			return err
		}
		if (ds.Scene == "academic_papers") != (nextScene == "academic_papers") {
			return resourceErr("CONFLICT", "cannot switch between paper and document knowledge bases")
		}
	}
	level := ds.ProcessingLevel
	if c.ProcessingLevel != nil {
		level = *c.ProcessingLevel
	}
	if err := validateAcademicScenario(ctx, tx, user, nextScene, level); err != nil {
		return resourceErr("INVALID_ARGUMENT", err.Error())
	}
	ext := knowledgeExt(ds.Ext)
	extChanged := false
	if c.CoverImage != nil {
		cover := strings.TrimSpace(*c.CoverImage)
		updates["cover_image"] = cover
		before["cover_image"], after["cover_image"] = ds.CoverImage, cover
	}
	if c.Tags != nil {
		tags := *c.Tags
		if c.TagMode == "append" {
			tags = append(parseDatasetTags(ds.Ext), tags...)
		} else if c.TagMode != "" && c.TagMode != "replace" {
			return resourceErr("INVALID_ARGUMENT", "tag_mode must be append or replace")
		}
		tags, err := normalizeAndValidateDatasetTags(tags)
		if err != nil {
			return err
		}
		if len(tags) == 0 {
			return resourceErr("INVALID_ARGUMENT", "at least one knowledge base tag is required")
		}
		before["tags"], after["tags"], ext["tags"] = parseDatasetTags(ds.Ext), tags, tags
		extChanged = true
	}
	if c.Recursive {
		return resourceErr("INVALID_ARGUMENT", "recursive applies only to folder document tags")
	}
	if c.AlgoID != nil && *c.AlgoID != parseDatasetAlgo(ds.Ext).AlgoID {
		before["algo_id"], after["algo_id"], ext["algo_id"] = ext["algo_id"], *c.AlgoID, *c.AlgoID
		if c.algorithm != nil {
			ext["algo_name"] = c.algorithm.DisplayName
			ext["parsers"] = c.parsers
		}
		extChanged = true
	}
	if c.PageParsers != nil {
		before["parsers"], after["parsers"] = ext["parsers"], *c.PageParsers
		ext["parsers"] = *c.PageParsers
		extChanged = true
	}
	if c.ReaderFallbackAccepted != nil {
		updates["reader_fallback_accepted"] = *c.ReaderFallbackAccepted
	}
	if c.ProcessingLevel != nil && *c.ProcessingLevel != effectiveProcessingLevel(ds.ProcessingLevel) {
		before["processing_level"], after["processing_level"] = ds.ProcessingLevel, *c.ProcessingLevel
		updates["processing_level"] = *c.ProcessingLevel
		updates["processing_revision"] = ds.ProcessingRevision + 1
		updates["transition_status"] = TransitionIdle
	}
	if c.Scene != nil || c.ProfileID != nil || c.Capabilities != nil {
		// Match the existing reading-configuration service's owner requirement.
		if ds.CreateUserID != user {
			return resourceErr("FORBIDDEN", "only the knowledge base owner can change reading configuration")
		}
		scene, err := resourceScene(ds.Scene)
		if err != nil {
			return err
		}
		profile, _ := ext["profile_id"].(string)
		if c.Scene != nil {
			if *c.Scene == "" {
				return resourceErr("INVALID_ARGUMENT", "scene cannot be empty")
			}
			scene, err = resourceScene(*c.Scene)
			if err != nil {
				return err
			}
			profile = ""
		}
		if c.ProfileID != nil {
			profile = *c.ProfileID
		}
		var refs []learning.CapabilityRef
		if c.Capabilities != nil {
			refs = *c.Capabilities
		}
		refs, err = knowledgeCapabilities(ctx, tx, user, scene, profile, refs)
		if err != nil {
			return err
		}
		if err := learning.SaveKnowledgeBaseCapabilities(tx, user, ds.ID, refs); err != nil {
			return err
		}
		after["capabilities"], after["profile_id"] = refs, profile
		ext["profile_id"], extChanged = profile, true
	}
	if extChanged {
		raw, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		updates["ext"] = raw
	}
	updates["resource_version"] = gorm.Expr("resource_version + 1")
	return nil
}

func KnowledgeOptions(ctx context.Context, db *gorm.DB, user string) (map[string]any, error) {
	profiles, err := learning.New(db).ListProfiles(ctx, user)
	if err != nil {
		return nil, err
	}
	algorithms, err := knowledgeAlgorithms(ctx)
	if err != nil {
		return nil, resourceErr("UNAVAILABLE", err.Error())
	}
	level, err := knowledgeProcessingLevel(ctx, db, user, "")
	if err != nil {
		return nil, err
	}
	levels := []string{ProcessingLevelStored}
	if level != ProcessingLevelStored {
		levels = append(levels, ProcessingLevelParsed, ProcessingLevelChunked)
	}
	if level == ProcessingLevelIndexed {
		levels = append(levels, ProcessingLevelIndexed)
	}
	parsers := map[string][]ParserConfig{}
	for _, algorithm := range algorithms {
		parsers[algorithm.AlgoID] = fetchParsersByAlgoID(ctx, algorithm.AlgoID)
	}
	defaultAlgoID := ""
	if systemdeps.PythonComponentActive("rag") {
		defaultAlgoID = defaultMarketAlgoID
	}
	return map[string]any{
		"parsers_by_algorithm": parsers,
		"scenes":               learning.BuiltinProfiles(), "profiles": profiles, "local_available": learning.LocalAvailable(),
		"capabilities": learning.Capabilities(), "algorithms": algorithms,
		"processing_levels": levels, "default_processing_level": level,
		"default_scene": "general", "default_algo_id": defaultAlgoID,
		"upload": map[string]any{"max_files": 20, "max_file_bytes": OriginalFileMaxBytes,
			"max_batch_bytes": OriginalBatchMaxBytes, "file_types": KnowledgeFileTypes},
	}, nil
}

func GetKnowledgeOptions(w http.ResponseWriter, r *http.Request) {
	out, err := KnowledgeOptions(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		replyResourceError(w, err)
		return
	}
	common.ReplyJSON(w, out)
}
