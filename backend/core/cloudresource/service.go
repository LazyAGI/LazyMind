package cloudresource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
)

var (
	ErrSessionRequired  = errors.New("LazyMind Cloud login is required")
	ErrPresenceConflict = errors.New("cloud resource cannot be downloaded in its current local state")
	uploadRequests      singleflight.Group
)

type TokenSource interface {
	AccessToken(context.Context, time.Duration) (string, error)
}

type CloudAPI interface {
	Origin() string
	GetCurrentAccount(context.Context, string) (cloudclient.Account, error)
	ListResources(context.Context, string, cloudclient.ResourceQuery) (cloudclient.ResourcePage, error)
	GetResource(context.Context, string, string) (cloudclient.PrivateResource, string, error)
	AuthorizeResourceDownload(context.Context, string, string, string) (cloudclient.DownloadAuthorization, error)
	DownloadSignedRequest(context.Context, cloudclient.SignedRequest, int64, string, int64) (cloudclient.DownloadedObject, error)
}

type BindingStore interface {
	FindByCloudIDs(context.Context, string, string, string, []string) (map[string]cloudbinding.Binding, error)
	FindByLocalID(context.Context, string, string, string, string) (cloudbinding.Binding, bool, error)
	Upsert(context.Context, cloudbinding.Binding) (cloudbinding.Binding, error)
}

type UploadCloudAPI interface {
	BeginResourceUpsert(context.Context, string, cloudclient.ResourceUpsertRequest) (cloudclient.ResourceOperation, error)
	PresignUploadParts(context.Context, string, string, []int) ([]cloudclient.UploadPartAuthorization, error)
	PutSignedFile(context.Context, cloudclient.SignedRequest, string, int64, int64) (string, error)
	CompleteResourceUpload(context.Context, string, string, []cloudclient.CompletedUploadPart) error
	GetResourceUpsertOperation(context.Context, string, string) (cloudclient.ResourceOperation, error)
	CancelResourceUpload(context.Context, string, string) error
}

type LocalSnapshot struct {
	Exists      bool
	ResourceID  string
	ResourceRef string
	RevisionID  string
	ContentHash string
}

type ImportRequest struct {
	OwnerUserID    string
	CloudIssuer    string
	CloudAccountID string
	Resource       cloudclient.PrivateResource
	Files          map[string]cloudpackage.File
}

type LocalAdapter interface {
	Probe(context.Context, string, string) (LocalSnapshot, error)
	FindExact(context.Context, string, string, string) (*LocalSnapshot, error)
	ImportAndBind(context.Context, ImportRequest) (LocalSnapshot, error)
}

type UploadAdapter interface {
	PrepareUpload(context.Context, string, string) (UploadPackage, error)
}

type UploadPackage struct {
	Local    LocalSnapshot
	Prepared cloudpackage.Prepared
}

type Service struct {
	Session          TokenSource
	Cloud            CloudAPI
	Bindings         BindingStore
	DesktopVersion   string
	MaxDownloadBytes int64
	MaxPolls         int
	Wait             func(context.Context, time.Duration) error
}

type UploadRequest struct {
	OwnerUserID     string
	ResourceType    string
	LocalResourceID string
	Adapter         UploadAdapter
}

type UploadResult struct {
	Status     cloudbinding.UploadStatus `json:"status"`
	ResourceID string                    `json:"resource_id,omitempty"`
}

func (s *Service) Upload(ctx context.Context, request UploadRequest) (UploadResult, error) {
	if s == nil || strings.TrimSpace(request.OwnerUserID) == "" || strings.TrimSpace(request.LocalResourceID) == "" ||
		(request.ResourceType != "skill" && request.ResourceType != "workflow") || request.Adapter == nil {
		return UploadResult{}, errors.New("cloud resource upload request is incomplete")
	}
	origin := ""
	if s.Cloud != nil {
		origin = s.Cloud.Origin()
	}
	key := origin + "\x00" + request.OwnerUserID + "\x00" + request.ResourceType + "\x00" + request.LocalResourceID
	value, err, _ := uploadRequests.Do(key, func() (any, error) {
		return s.upload(ctx, request)
	})
	if err != nil {
		return UploadResult{}, err
	}
	result, ok := value.(UploadResult)
	if !ok {
		return UploadResult{}, errors.New("cloud resource upload returned an invalid result")
	}
	return result, nil
}

func (s *Service) upload(ctx context.Context, request UploadRequest) (UploadResult, error) {
	if s.Bindings == nil || s.Cloud == nil {
		return UploadResult{}, errors.New("cloud resource upload is not configured")
	}
	uploadCloud, ok := s.Cloud.(UploadCloudAPI)
	if !ok {
		return UploadResult{}, errors.New("LazyMind Cloud upload client is unavailable")
	}
	token, account, err := s.cloudContext(ctx)
	if err != nil {
		return UploadResult{}, err
	}
	exported, err := request.Adapter.PrepareUpload(ctx, request.OwnerUserID, request.LocalResourceID)
	if err != nil {
		return UploadResult{}, err
	}
	if !exported.Local.Exists || exported.Local.ResourceID != request.LocalResourceID || exported.Local.ContentHash == "" ||
		exported.Prepared.Manifest.ResourceType != request.ResourceType || exported.Prepared.Manifest.ContentHash != exported.Local.ContentHash || exported.Prepared.ZIPPath != "" {
		return UploadResult{}, errors.New("local Cloud resource package is inconsistent")
	}

	binding, hasBinding, err := s.Bindings.FindByLocalID(ctx, s.Cloud.Origin(), account.ID, request.ResourceType, request.LocalResourceID)
	if err != nil {
		return UploadResult{}, err
	}
	var current *cloudclient.PrivateResource
	if hasBinding {
		resource, _, getErr := s.Cloud.GetResource(ctx, token, binding.CloudResourceID)
		if getErr != nil {
			return UploadResult{}, getErr
		}
		current = &resource
	}
	candidates := []cloudbinding.CloudResource{}
	if !hasBinding {
		resources, listErr := s.listAllResources(ctx, token, request.ResourceType)
		if listErr != nil {
			return UploadResult{}, listErr
		}
		for _, resource := range resources {
			candidates = append(candidates, cloudbinding.CloudResource{
				ID: resource.ResourceID, Type: resource.ResourceType, Name: resource.ResourceName, ContentHash: resource.ContentHash,
			})
		}
	}
	compatible := exported.Prepared.Manifest.FormatSchema == cloudpackage.FormatSchemaV2
	decisionInput := cloudbinding.UploadInput{
		Binding: func() *cloudbinding.Binding {
			if hasBinding {
				copy := binding
				return &copy
			}
			return nil
		}(),
		Local:     cloudbinding.LocalProbe{Exists: true, ContentHash: exported.Local.ContentHash},
		LocalName: exported.Prepared.Manifest.ResourceName, LocalType: request.ResourceType, Candidates: candidates, Compatible: &compatible,
	}
	if current != nil {
		decisionInput.Cloud = &cloudbinding.CloudResource{ID: current.ResourceID, Type: current.ResourceType, Name: current.ResourceName, ContentHash: current.ContentHash}
	}
	decision := cloudbinding.ResolveUpload(decisionInput)
	if decision.AdoptResourceID != "" {
		for _, candidate := range candidates {
			if candidate.ID == decision.AdoptResourceID {
				_, err := s.Bindings.Upsert(ctx, bindingAfterUpload(candidate.ID, exported, s.Cloud.Origin(), account.ID))
				return UploadResult{Status: decision.Status, ResourceID: candidate.ID}, err
			}
		}
		return UploadResult{}, errors.New("Cloud adoption candidate disappeared")
	}
	if !decision.CallUpsert || !decision.CreateZIP {
		resourceID := ""
		if current != nil {
			resourceID = current.ResourceID
		}
		return UploadResult{Status: decision.Status, ResourceID: resourceID}, nil
	}

	zipped, err := cloudpackage.WriteZIP(exported.Prepared)
	if err != nil {
		return UploadResult{}, err
	}
	defer os.Remove(zipped.ZIPPath)
	condition := cloudclient.ResourceUpsertRequest{
		ResourceType: request.ResourceType, ClientResourceKey: zipped.Manifest.ClientResourceKey, Manifest: zipped.Manifest,
		IdempotencyKey: uploadIdempotencyKey(account.ID, request.LocalResourceID, zipped.Manifest.ContentHash),
		IfNoneMatch:    decision.Status == cloudbinding.UploadFirst,
	}
	if decision.Status == cloudbinding.UploadUpdateAvailable && current != nil {
		condition.IfMatch = current.ContentHash
	}
	operation, err := uploadCloud.BeginResourceUpsert(ctx, token, condition)
	if err != nil {
		return UploadResult{}, err
	}
	if operation.Status == cloudclient.ResourceOperationSucceeded {
		return s.finishUploadBinding(ctx, decision.Status, operation.Resource, exported, s.Cloud.Origin(), account.ID)
	}
	if operation.Status != cloudclient.ResourceOperationWaitingForUpload || operation.Upload == nil {
		return UploadResult{}, errors.New("LazyMind Cloud did not initialize the resource upload")
	}
	uploadID := operation.Upload.UploadID
	completed := false
	defer func() {
		if !completed {
			_ = uploadCloud.CancelResourceUpload(context.WithoutCancel(ctx), token, uploadID)
		}
	}()
	parts, err := uploadPackageBytes(ctx, uploadCloud, token, zipped, *operation.Upload)
	if err != nil {
		return UploadResult{}, err
	}
	completeErr := uploadCloud.CompleteResourceUpload(ctx, token, uploadID, parts)
	final, err := s.waitForUpsert(ctx, uploadCloud, token, operation.OperationID)
	if err != nil {
		if completeErr != nil {
			return UploadResult{}, fmt.Errorf("complete Cloud upload: %w", completeErr)
		}
		return UploadResult{}, err
	}
	completed = true
	result, err := s.finishUploadBinding(ctx, decision.Status, final.Resource, exported, s.Cloud.Origin(), account.ID)
	return result, err
}

type ListRequest struct {
	OwnerUserID  string
	ResourceType string
	Cursor       string
	PageSize     int
	Adapter      LocalAdapter
}

type ListItem struct {
	ResourceID       string                      `json:"resource_id"`
	ResourceType     string                      `json:"resource_type"`
	ResourceName     string                      `json:"resource_name"`
	ContentSize      int64                       `json:"content_size"`
	FormatSchema     string                      `json:"format_schema"`
	UpdatedAt        string                      `json:"updated_at"`
	PresenceStatus   cloudbinding.PresenceStatus `json:"presence_status"`
	LocalExists      bool                        `json:"local_exists"`
	LocalResourceID  string                      `json:"local_resource_id,omitempty"`
	LocalResourceRef string                      `json:"local_resource_ref,omitempty"`
}

type ListPage struct {
	Items      []ListItem `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func (s Service) List(ctx context.Context, request ListRequest) (ListPage, error) {
	if err := validateRequest(s, request.OwnerUserID, request.ResourceType, request.Adapter); err != nil {
		return ListPage{}, err
	}
	token, account, err := s.cloudContext(ctx)
	if err != nil {
		return ListPage{}, err
	}
	page, err := s.Cloud.ListResources(ctx, token, cloudclient.ResourceQuery{
		ResourceType: request.ResourceType, Cursor: request.Cursor, PageSize: request.PageSize,
	})
	if err != nil {
		return ListPage{}, err
	}
	resourceIDs := make([]string, 0, len(page.Items))
	for _, resource := range page.Items {
		if resource.ResourceType != request.ResourceType {
			return ListPage{}, errors.New("cloud resource type does not match the requested collection")
		}
		resourceIDs = append(resourceIDs, resource.ResourceID)
	}
	bindings, err := s.Bindings.FindByCloudIDs(ctx, s.Cloud.Origin(), account.ID, request.ResourceType, resourceIDs)
	if err != nil {
		return ListPage{}, err
	}
	items := make([]ListItem, 0, len(page.Items))
	for _, resource := range page.Items {
		binding := bindings[resource.ResourceID]
		var bindingPtr *cloudbinding.Binding
		local := LocalSnapshot{}
		if binding.CloudResourceID != "" {
			bindingPtr = &binding
			local, err = request.Adapter.Probe(ctx, request.OwnerUserID, binding.LocalResourceID)
			if err != nil {
				return ListPage{}, err
			}
		} else {
			candidate, findErr := request.Adapter.FindExact(ctx, request.OwnerUserID, resource.ResourceName, resource.ContentHash)
			if findErr != nil {
				return ListPage{}, findErr
			}
			if candidate != nil && candidate.Exists {
				local = *candidate
				adopted, upsertErr := s.Bindings.Upsert(ctx, bindingFor(resource, s.Cloud.Origin(), account.ID, local))
				if upsertErr != nil {
					return ListPage{}, upsertErr
				}
				bindingPtr = &adopted
			}
		}
		compatible := resource.FormatSchema == cloudpackage.FormatSchemaV2
		decision := cloudbinding.ResolvePresence(cloudbinding.PresenceInput{
			Cloud:   cloudbinding.CloudResource{ID: resource.ResourceID, Type: resource.ResourceType, Name: resource.ResourceName, ContentHash: resource.ContentHash},
			Binding: bindingPtr, Local: cloudbinding.LocalProbe{Exists: local.Exists, ContentHash: local.ContentHash}, Compatible: &compatible,
		})
		items = append(items, ListItem{
			ResourceID: resource.ResourceID, ResourceType: resource.ResourceType, ResourceName: resource.ResourceName,
			ContentSize: resource.ContentSize, FormatSchema: resource.FormatSchema, UpdatedAt: resource.UpdatedAt,
			PresenceStatus: decision.Status, LocalExists: local.Exists,
			LocalResourceID: local.ResourceID, LocalResourceRef: local.ResourceRef,
		})
	}
	return ListPage{Items: items, NextCursor: page.NextCursor}, nil
}

type DownloadRequest struct {
	OwnerUserID  string
	ResourceType string
	ResourceID   string
	Adapter      LocalAdapter
}

type DownloadResult struct {
	ResourceID       string `json:"resource_id"`
	LocalResourceID  string `json:"local_resource_id"`
	LocalResourceRef string `json:"local_resource_ref,omitempty"`
	AlreadyPresent   bool   `json:"already_present"`
}

func (s Service) Download(ctx context.Context, request DownloadRequest) (DownloadResult, error) {
	if err := validateRequest(s, request.OwnerUserID, request.ResourceType, request.Adapter); err != nil {
		return DownloadResult{}, err
	}
	token, account, err := s.cloudContext(ctx)
	if err != nil {
		return DownloadResult{}, err
	}
	resource, etag, err := s.Cloud.GetResource(ctx, token, strings.TrimSpace(request.ResourceID))
	if err != nil {
		return DownloadResult{}, err
	}
	if resource.ResourceType != request.ResourceType {
		return DownloadResult{}, errors.New("cloud resource type does not match the requested collection")
	}
	bindings, err := s.Bindings.FindByCloudIDs(ctx, s.Cloud.Origin(), account.ID, request.ResourceType, []string{resource.ResourceID})
	if err != nil {
		return DownloadResult{}, err
	}
	binding := bindings[resource.ResourceID]
	var bindingPtr *cloudbinding.Binding
	local := LocalSnapshot{}
	if binding.CloudResourceID != "" {
		bindingPtr = &binding
		local, err = request.Adapter.Probe(ctx, request.OwnerUserID, binding.LocalResourceID)
		if err != nil {
			return DownloadResult{}, err
		}
	} else {
		candidate, findErr := request.Adapter.FindExact(ctx, request.OwnerUserID, resource.ResourceName, resource.ContentHash)
		if findErr != nil {
			return DownloadResult{}, findErr
		}
		if candidate != nil && candidate.Exists {
			local = *candidate
			adopted, upsertErr := s.Bindings.Upsert(ctx, bindingFor(resource, s.Cloud.Origin(), account.ID, local))
			if upsertErr != nil {
				return DownloadResult{}, upsertErr
			}
			bindingPtr = &adopted
		}
	}
	compatible := resource.FormatSchema == cloudpackage.FormatSchemaV2
	decision := cloudbinding.ResolvePresence(cloudbinding.PresenceInput{
		Cloud:   cloudbinding.CloudResource{ID: resource.ResourceID, Type: resource.ResourceType, Name: resource.ResourceName, ContentHash: resource.ContentHash},
		Binding: bindingPtr, Local: cloudbinding.LocalProbe{Exists: local.Exists, ContentHash: local.ContentHash}, Compatible: &compatible,
	})
	if decision.Status == cloudbinding.PresencePresentCurrent {
		return DownloadResult{ResourceID: resource.ResourceID, LocalResourceID: local.ResourceID, LocalResourceRef: local.ResourceRef, AlreadyPresent: true}, nil
	}
	if decision.Status != cloudbinding.PresenceDownloadRequired && decision.Status != cloudbinding.PresenceLocalMissing {
		return DownloadResult{}, fmt.Errorf("%w: %s", ErrPresenceConflict, decision.Status)
	}
	authorization, err := s.waitForDownloadAuthorization(ctx, token, resource.ResourceID, etag)
	if err != nil {
		return DownloadResult{}, err
	}
	if authorization.Request == nil {
		return DownloadResult{}, errors.New("LazyMind Cloud download authorization omitted the signed request")
	}
	downloaded, err := s.Cloud.DownloadSignedRequest(ctx, *authorization.Request, 0, "", s.maxDownloadBytes())
	if err != nil {
		return DownloadResult{}, err
	}
	defer os.Remove(downloaded.Path)
	prepared, err := cloudpackage.ReadAndVerifyZIP(cloudpackage.VerifyZIPInput{
		ZIPPath: downloaded.Path, ResourceType: resource.ResourceType, ResourceName: resource.ResourceName,
		ClientResourceKey: resource.ClientResourceKey, DesktopVersion: s.desktopVersion(),
		ExpectedContentHash: resource.ContentHash, ExpectedContentSize: resource.ContentSize,
	})
	if err != nil {
		return DownloadResult{}, err
	}
	imported, err := request.Adapter.ImportAndBind(ctx, ImportRequest{
		OwnerUserID: request.OwnerUserID, CloudIssuer: s.Cloud.Origin(), CloudAccountID: account.ID,
		Resource: resource, Files: prepared.Files,
	})
	if err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{ResourceID: resource.ResourceID, LocalResourceID: imported.ResourceID, LocalResourceRef: imported.ResourceRef}, nil
}

func (s Service) cloudContext(ctx context.Context) (string, cloudclient.Account, error) {
	if s.Session == nil || s.Cloud == nil {
		return "", cloudclient.Account{}, ErrSessionRequired
	}
	token, err := s.Session.AccessToken(ctx, 30*time.Second)
	if err != nil {
		return "", cloudclient.Account{}, fmt.Errorf("%w: %v", ErrSessionRequired, err)
	}
	account, err := s.Cloud.GetCurrentAccount(ctx, token)
	return token, account, err
}

func (s Service) waitForDownloadAuthorization(ctx context.Context, token, resourceID, etag string) (cloudclient.DownloadAuthorization, error) {
	polls := s.MaxPolls
	if polls <= 0 {
		polls = 5
	}
	for attempt := 0; attempt < polls; attempt++ {
		authorization, err := s.Cloud.AuthorizeResourceDownload(ctx, token, resourceID, etag)
		if err != nil {
			return cloudclient.DownloadAuthorization{}, err
		}
		if authorization.Status == cloudclient.DownloadReady {
			return authorization, nil
		}
		if authorization.Status != cloudclient.DownloadPreparing {
			return cloudclient.DownloadAuthorization{}, errors.New("LazyMind Cloud returned an unknown download status")
		}
		seconds := authorization.RetryAfterSeconds
		if seconds <= 0 {
			seconds = 1
		}
		if seconds > 10 {
			seconds = 10
		}
		wait := s.Wait
		if wait == nil {
			wait = waitContext
		}
		if err := wait(ctx, time.Duration(seconds)*time.Second); err != nil {
			return cloudclient.DownloadAuthorization{}, err
		}
	}
	return cloudclient.DownloadAuthorization{}, errors.New("LazyMind Cloud download preparation timed out")
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validateRequest(service Service, owner, resourceType string, adapter LocalAdapter) error {
	if service.Bindings == nil || adapter == nil || strings.TrimSpace(owner) == "" {
		return errors.New("cloud resource request is incomplete")
	}
	if resourceType != "skill" && resourceType != "workflow" {
		return errors.New("cloud resource type must be skill or workflow")
	}
	return nil
}

func bindingFor(resource cloudclient.PrivateResource, issuer, accountID string, local LocalSnapshot) cloudbinding.Binding {
	return cloudbinding.Binding{
		CloudIssuer: issuer, CloudAccountID: accountID, ResourceType: resource.ResourceType,
		CloudResourceID: resource.ResourceID, ClientResourceKey: resource.ClientResourceKey,
		CloudContentHash: resource.ContentHash, LocalResourceID: local.ResourceID, LocalResourceRef: local.ResourceRef,
		InstalledLocalRevisionID: local.RevisionID, InstalledLocalContentHash: local.ContentHash,
		CloudResourceName: resource.ResourceName,
	}
}

func (s Service) desktopVersion() string {
	value := strings.TrimSpace(s.DesktopVersion)
	if value == "" {
		return "0.0.0"
	}
	return value
}

func (s Service) maxDownloadBytes() int64 {
	if s.MaxDownloadBytes > 0 {
		return s.MaxDownloadBytes
	}
	return 100 << 20
}

func (s *Service) listAllResources(ctx context.Context, token, resourceType string) ([]cloudclient.PrivateResource, error) {
	items := []cloudclient.PrivateResource{}
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := s.Cloud.ListResources(ctx, token, cloudclient.ResourceQuery{ResourceType: resourceType, Cursor: cursor, PageSize: 100})
		if err != nil {
			return nil, err
		}
		items = append(items, result.Items...)
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			return items, nil
		}
	}
	return nil, errors.New("LazyMind Cloud resource listing exceeded the page limit")
}

func uploadPackageBytes(ctx context.Context, cloud UploadCloudAPI, token string, prepared cloudpackage.Prepared, upload cloudclient.UploadAuthorization) ([]cloudclient.CompletedUploadPart, error) {
	switch upload.Mode {
	case cloudclient.UploadModeSinglePut:
		if upload.SignedRequest == nil {
			return nil, errors.New("single PUT upload omitted its signed request")
		}
		_, err := cloud.PutSignedFile(ctx, *upload.SignedRequest, prepared.ZIPPath, 0, prepared.Manifest.TransportSize)
		return nil, err
	case cloudclient.UploadModeMultipart:
		if upload.PartSize <= 0 {
			return nil, errors.New("multipart upload omitted part_size")
		}
		count := int((prepared.Manifest.TransportSize + upload.PartSize - 1) / upload.PartSize)
		if count < 1 || count > 10000 {
			return nil, errors.New("multipart upload part count is invalid")
		}
		completed := make([]cloudclient.CompletedUploadPart, 0, count)
		for start := 1; start <= count; start += 50 {
			end := min(start+49, count)
			numbers := make([]int, 0, end-start+1)
			for number := start; number <= end; number++ {
				numbers = append(numbers, number)
			}
			authorizations, err := cloud.PresignUploadParts(ctx, token, upload.UploadID, numbers)
			if err != nil {
				return nil, err
			}
			for _, authorization := range authorizations {
				offset := int64(authorization.PartNumber-1) * upload.PartSize
				length := min(upload.PartSize, prepared.Manifest.TransportSize-offset)
				etag, err := cloud.PutSignedFile(ctx, authorization.SignedRequest, prepared.ZIPPath, offset, length)
				if err != nil {
					return nil, err
				}
				if strings.TrimSpace(etag) == "" {
					return nil, errors.New("multipart upload response omitted ETag")
				}
				completed = append(completed, cloudclient.CompletedUploadPart{PartNumber: authorization.PartNumber, ETag: etag})
			}
		}
		return completed, nil
	default:
		return nil, errors.New("LazyMind Cloud returned an unsupported upload mode")
	}
}

func (s *Service) waitForUpsert(ctx context.Context, cloud UploadCloudAPI, token, operationID string) (cloudclient.ResourceOperation, error) {
	polls := s.MaxPolls
	if polls <= 0 {
		polls = 60
	}
	wait := s.Wait
	if wait == nil {
		wait = waitContext
	}
	for attempt := 0; attempt < polls; attempt++ {
		operation, err := cloud.GetResourceUpsertOperation(ctx, token, operationID)
		if err != nil {
			return cloudclient.ResourceOperation{}, err
		}
		switch operation.Status {
		case cloudclient.ResourceOperationSucceeded:
			if operation.Resource == nil {
				return cloudclient.ResourceOperation{}, errors.New("successful Cloud upsert omitted the resource")
			}
			return operation, nil
		case cloudclient.ResourceOperationWaitingForUpload, cloudclient.ResourceOperationVerifying:
			if err := wait(ctx, time.Second); err != nil {
				return cloudclient.ResourceOperation{}, err
			}
		case cloudclient.ResourceOperationFailed, cloudclient.ResourceOperationConflicted,
			cloudclient.ResourceOperationCancelled, cloudclient.ResourceOperationExpired:
			return cloudclient.ResourceOperation{}, fmt.Errorf("LazyMind Cloud upload ended with status %s", operation.Status)
		default:
			return cloudclient.ResourceOperation{}, errors.New("LazyMind Cloud returned an unknown upload status")
		}
	}
	return cloudclient.ResourceOperation{}, errors.New("LazyMind Cloud upload verification timed out")
}

func (s *Service) finishUploadBinding(ctx context.Context, status cloudbinding.UploadStatus, resource *cloudclient.PrivateResource, exported UploadPackage, issuer, accountID string) (UploadResult, error) {
	if resource == nil || resource.ContentHash != exported.Prepared.Manifest.ContentHash || resource.ResourceType != exported.Prepared.Manifest.ResourceType {
		return UploadResult{}, errors.New("LazyMind Cloud upload result does not match the local package")
	}
	_, err := s.Bindings.Upsert(ctx, bindingAfterUpload(resource.ResourceID, exported, issuer, accountID))
	if err != nil {
		return UploadResult{}, fmt.Errorf("Cloud upload succeeded but local binding failed: %w", err)
	}
	return UploadResult{Status: status, ResourceID: resource.ResourceID}, nil
}

func bindingAfterUpload(resourceID string, exported UploadPackage, issuer, accountID string) cloudbinding.Binding {
	return cloudbinding.Binding{
		CloudIssuer: issuer, CloudAccountID: accountID, ResourceType: exported.Prepared.Manifest.ResourceType,
		CloudResourceID: resourceID, ClientResourceKey: exported.Prepared.Manifest.ClientResourceKey,
		CloudContentHash: exported.Prepared.Manifest.ContentHash, LocalResourceID: exported.Local.ResourceID,
		LocalResourceRef: exported.Local.ResourceRef, InstalledLocalRevisionID: exported.Local.RevisionID,
		InstalledLocalContentHash: exported.Local.ContentHash, CloudResourceName: exported.Prepared.Manifest.ResourceName,
	}
}

func uploadIdempotencyKey(accountID, localResourceID, contentHash string) string {
	value := "resource-upload:" + accountID + ":" + localResourceID + ":" + contentHash
	if len(value) <= 128 {
		return value
	}
	return "resource-upload:" + contentHash
}
