package taskdisplay

import (
	"encoding/json"
	"strings"
)

type MissingCapability struct {
	ID          string `json:"id" enum:"image_generator,image_editor,video_generator,ffmpeg"`
	Label       string `json:"label"`
	Available   bool   `json:"available"`
	SettingsURL string `json:"settings_url"`
	Reason      string `json:"reason"`
}

// CapabilityDependency exposes setup actions, never executor error text.
type CapabilityDependency struct {
	Status   string              `json:"status" enum:"blocked"`
	Required []string            `json:"required" required:"true"`
	Missing  []MissingCapability `json:"missing" required:"true"`
	Message  string              `json:"message"`
}

var mediaCapabilities = map[string]MissingCapability{
	"image_generator": {ID: "image_generator", Label: "文生图模型", SettingsURL: "/settings?section=models&target=image_generator", Reason: "请配置可用的文生图模型。"},
	"image_editor":    {ID: "image_editor", Label: "图片编辑模型", SettingsURL: "/settings?section=models&target=image_editor", Reason: "请配置可用的图片编辑模型。"},
	"video_generator": {ID: "video_generator", Label: "视频生成模型", SettingsURL: "/settings?section=models&target=video_generator", Reason: "请配置可用的视频生成模型。"},
	"ffmpeg":          {ID: "ffmpeg", Label: "FFmpeg", SettingsURL: "/settings?section=system_tools#ffmpeg-dependency", Reason: "请配置可用的 FFmpeg/FFprobe。"},
}

// CapabilityRecovery reads only a bounded, explicitly marked failure. Labels,
// links and descriptions come from the allowlist, not the untrusted summary.
func CapabilityRecovery(summary string) *CapabilityDependency {
	const marker = "MEDIA_CAPABILITY_DEPENDENCY_MISSING"
	if len(summary) > 64*1024 {
		return nil
	}
	index := strings.Index(summary, marker)
	if index < 0 {
		return nil
	}
	var payload struct {
		Status   string   `json:"status"`
		Required []string `json:"required"`
		Missing  []struct {
			ID        string `json:"id"`
			Available *bool  `json:"available"`
		} `json:"missing"`
	}
	// Decode one object so an exception's suffix cannot become public text.
	if json.NewDecoder(strings.NewReader(strings.TrimSpace(summary[index+len(marker):]))).Decode(&payload) != nil || payload.Status != "blocked" {
		return nil
	}
	out := &CapabilityDependency{Status: "blocked", Required: []string{}, Missing: []MissingCapability{}}
	required, missing := map[string]bool{}, map[string]bool{}
	for _, id := range payload.Required {
		if _, ok := mediaCapabilities[id]; ok && !required[id] {
			required[id] = true
			out.Required = append(out.Required, id)
		}
	}
	labels := []string{}
	for _, item := range payload.Missing {
		capability, ok := mediaCapabilities[item.ID]
		if !ok || missing[item.ID] || item.Available == nil || *item.Available {
			continue
		}
		missing[item.ID] = true
		out.Missing = append(out.Missing, capability)
		labels = append(labels, capability.Label)
		if !required[item.ID] {
			required[item.ID] = true
			out.Required = append(out.Required, item.ID)
		}
	}
	if len(out.Missing) == 0 {
		return nil
	}
	out.Message = "当前任务缺少：" + strings.Join(labels, "、") + "。请完成配置后继续。"
	return out
}
