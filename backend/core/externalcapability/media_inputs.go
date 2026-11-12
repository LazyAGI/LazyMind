package externalcapability

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"lazymind/core/common/orm"
)

const maxExternalMediaInputBytes = 20 << 20

// Only immutable resources owned by the authenticated user cross this boundary.
// External paths/URLs are never passed to the media tool's legacy file resolver.
func (s *Service) resolveMediaInputs(ctx context.Context, userID, name string, arguments map[string]any) (map[string]any, error) {
	if name != "image_editor" && name != "video_generator" && name != "multimodal" {
		return arguments, nil
	}
	result := make(map[string]any, len(arguments))
	for key, value := range arguments {
		result[key] = value
	}
	total := 0
	cache := map[string]string{}
	resolve := func(value any) (string, error) {
		ref, ok := value.(string)
		if !ok || !strings.HasPrefix(ref, "resource:") || strings.TrimSpace(strings.TrimPrefix(ref, "resource:")) == "" {
			return "", fmt.Errorf("media inputs must use resource:<id> from workflow.input.import")
		}
		if cached, ok := cache[ref]; ok {
			return cached, nil
		}
		var resource orm.WorkflowInputResource
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", strings.TrimPrefix(ref, "resource:"), userID).First(&resource).Error; err != nil {
			return "", fmt.Errorf("media input resource is unavailable to this user")
		}
		if resource.Size != int64(len(resource.Content)) || len(resource.Content) == 0 || len(resource.Content) > maxExternalMediaInputBytes-total {
			return "", fmt.Errorf("media inputs are empty or exceed 20 MiB")
		}
		if resource.ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256(resource.Content)) {
			return "", fmt.Errorf("media input resource content hash does not match")
		}
		mimeType := http.DetectContentType(resource.Content)
		switch mimeType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return "", fmt.Errorf("media conditioning requires a PNG, JPEG, GIF or WebP image")
		}
		total += len(resource.Content)
		encoded := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(resource.Content)
		cache[ref] = encoded
		return encoded, nil
	}
	if name == "multimodal" {
		image, err := resolve(result["url"])
		if err != nil {
			return nil, err
		}
		result["url"] = image
		return result, nil
	}
	for _, key := range []string{"urls", "reference_urls", "first_frame_url", "last_frame_url"} {
		value, present := result[key]
		if !present || value == nil || value == "" {
			continue
		}
		if values, ok := value.([]any); ok {
			resolved := make([]string, 0, len(values))
			for _, item := range values {
				ref, err := resolve(item)
				if err != nil {
					return nil, err
				}
				resolved = append(resolved, ref)
			}
			result[key] = resolved
		} else if values, ok := value.([]string); ok {
			resolved := make([]string, 0, len(values))
			for _, item := range values {
				ref, err := resolve(item)
				if err != nil {
					return nil, err
				}
				resolved = append(resolved, ref)
			}
			result[key] = resolved
		} else {
			ref, err := resolve(value)
			if err != nil {
				return nil, err
			}
			result[key] = ref
		}
	}
	return result, nil
}
