package cloudbinding

import "time"

type CloudResource struct {
	ID          string
	Type        string
	Name        string
	ContentHash string
}

type Binding struct {
	ID                        string    `gorm:"column:id;type:varchar(36);primaryKey"`
	CloudIssuer               string    `gorm:"column:cloud_issuer;type:varchar(512);not null;uniqueIndex:uk_cloud_binding_resource,priority:1;uniqueIndex:uk_cloud_binding_local,priority:1"`
	CloudAccountID            string    `gorm:"column:cloud_account_id;type:varchar(255);not null;uniqueIndex:uk_cloud_binding_resource,priority:2;uniqueIndex:uk_cloud_binding_local,priority:2"`
	ResourceType              string    `gorm:"column:resource_type;type:varchar(16);not null;uniqueIndex:uk_cloud_binding_resource,priority:3;uniqueIndex:uk_cloud_binding_local,priority:3"`
	CloudResourceID           string    `gorm:"column:cloud_resource_id;type:varchar(128);not null;uniqueIndex:uk_cloud_binding_resource,priority:4"`
	ClientResourceKey         string    `gorm:"column:client_resource_key;type:varchar(128);not null"`
	CloudContentHash          string    `gorm:"column:cloud_content_hash;type:varchar(64);not null"`
	LocalResourceID           string    `gorm:"column:local_resource_id;type:varchar(128);not null;uniqueIndex:uk_cloud_binding_local,priority:4"`
	LocalResourceRef          string    `gorm:"column:local_resource_ref;type:varchar(512);not null;default:''"`
	InstalledLocalRevisionID  string    `gorm:"column:installed_local_revision_id;type:varchar(64);not null"`
	InstalledLocalContentHash string    `gorm:"column:installed_local_content_hash;type:varchar(64);not null"`
	CloudResourceName         string    `gorm:"column:cloud_resource_name;type:varchar(255);not null"`
	CreatedAt                 time.Time `gorm:"column:created_at;not null"`
	UpdatedAt                 time.Time `gorm:"column:updated_at;not null"`
}

func (Binding) TableName() string { return "cloud_resource_bindings" }

type LocalProbe struct {
	Exists      bool
	ContentHash string
}

type PresenceStatus string

const (
	PresencePresentCurrent   PresenceStatus = "present_current"
	PresenceDownloadRequired PresenceStatus = "download_required"
	PresenceLocalMissing     PresenceStatus = "local_missing"
	PresenceCloudUpdated     PresenceStatus = "cloud_updated"
	PresenceLocalModified    PresenceStatus = "local_modified"
	PresenceDiverged         PresenceStatus = "diverged"
	PresenceIncompatible     PresenceStatus = "incompatible"
)

type PresenceInput struct {
	Cloud      CloudResource
	Binding    *Binding
	Local      LocalProbe
	Compatible *bool
}

type PresenceDecision struct {
	Status          PresenceStatus
	LocalResourceID string
}

func ResolvePresence(input PresenceInput) PresenceDecision {
	if input.Compatible != nil && !*input.Compatible {
		return PresenceDecision{Status: PresenceIncompatible}
	}
	if input.Binding == nil || input.Binding.CloudResourceID != input.Cloud.ID {
		return PresenceDecision{Status: PresenceDownloadRequired}
	}
	if !input.Local.Exists {
		return PresenceDecision{Status: PresenceLocalMissing}
	}
	cloudChanged := input.Cloud.ContentHash != input.Binding.CloudContentHash
	localChanged := input.Local.ContentHash != input.Binding.InstalledLocalContentHash
	switch {
	case cloudChanged && localChanged:
		return PresenceDecision{Status: PresenceDiverged, LocalResourceID: input.Binding.LocalResourceID}
	case cloudChanged:
		return PresenceDecision{Status: PresenceCloudUpdated, LocalResourceID: input.Binding.LocalResourceID}
	case localChanged:
		return PresenceDecision{Status: PresenceLocalModified, LocalResourceID: input.Binding.LocalResourceID}
	default:
		return PresenceDecision{Status: PresencePresentCurrent, LocalResourceID: input.Binding.LocalResourceID}
	}
}

type UploadStatus string

const (
	UploadNotRequired     UploadStatus = "upload_not_required"
	UploadFirst           UploadStatus = "upload_first"
	UploadUpdateAvailable UploadStatus = "upload_update_available"
	UploadCloudUpdated    UploadStatus = "cloud_updated"
	UploadDiverged        UploadStatus = "diverged"
	UploadIncompatible    UploadStatus = "incompatible"
)

type UploadInput struct {
	Cloud      *CloudResource
	Binding    *Binding
	Local      LocalProbe
	LocalName  string
	LocalType  string
	Candidates []CloudResource
	Compatible *bool
}

type UploadDecision struct {
	Status          UploadStatus
	CreateZIP       bool
	CallUpsert      bool
	AdoptResourceID string
}

func ResolveUpload(input UploadInput) UploadDecision {
	if input.Compatible != nil && !*input.Compatible {
		return UploadDecision{Status: UploadIncompatible}
	}
	if input.Binding != nil && input.Cloud != nil && input.Binding.CloudResourceID == input.Cloud.ID {
		cloudChanged := input.Cloud.ContentHash != input.Binding.CloudContentHash
		localChanged := input.Local.ContentHash != input.Binding.InstalledLocalContentHash
		switch {
		case cloudChanged && localChanged:
			return UploadDecision{Status: UploadDiverged}
		case cloudChanged:
			return UploadDecision{Status: UploadCloudUpdated}
		case localChanged:
			return UploadDecision{Status: UploadUpdateAvailable, CreateZIP: true, CallUpsert: true}
		default:
			return UploadDecision{Status: UploadNotRequired}
		}
	}
	matchingID := ""
	for _, candidate := range input.Candidates {
		if candidate.Name != input.LocalName || candidate.ContentHash != input.Local.ContentHash {
			continue
		}
		if input.LocalType != "" && candidate.Type != input.LocalType {
			continue
		}
		if matchingID != "" {
			matchingID = ""
			break
		}
		matchingID = candidate.ID
	}
	if matchingID != "" {
		return UploadDecision{Status: UploadNotRequired, AdoptResourceID: matchingID}
	}
	return UploadDecision{Status: UploadFirst, CreateZIP: true, CallUpsert: true}
}
