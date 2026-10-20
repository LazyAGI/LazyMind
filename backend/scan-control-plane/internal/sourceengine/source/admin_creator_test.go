package source

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type creatorResolverStub struct {
	identity CreatorIdentity
	err      error
	calls    int
	tenantID string
}

func (r *creatorResolverStub) ResolveAdmin(_ context.Context, tenantID string) (CreatorIdentity, error) {
	r.calls++
	r.tenantID = tenantID
	return r.identity, r.err
}

func adminCreatorRequest() CreateSourceRequest {
	return CreateSourceRequest{
		CallerID: "requester-1", CallerName: "Requester", TenantID: "tenant-1",
		RequestID: "request-1", Name: "Admin-owned_docs",
		Bindings: []BindingInput{{ConnectorType: spyConnectorType, TargetType: spyTargetType,
			TargetRef: "raw-target", SyncMode: SyncModeManual}},
		SourceOptions: map[string]any{"source_type": "feishu", "created_by_name": "spoofed-name"},
	}
}

func newAdminCreatorEngine(t *testing.T, resolver *creatorResolverStub) (*DefaultEngine, *sourceEngineRepoStub, *sourceCoreSpy, *sourceSpyConnector) {
	t.Helper()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	conn := &sourceSpyConnector{target: connector.NormalizedTarget{
		TargetType: spyTargetType, TargetRef: "normalized-target", TargetFingerprint: "target-fingerprint", RootObjectKey: "root", DisplayName: "Synthetic_root",
	}}
	engine := newTestSourceEngine(t, repo, core, conn, fixedSourceTestTime())
	engine.repo = creatorUpdateRepo{repo}
	if resolver == nil {
		WithCreatorResolver(nil)(engine)
	} else {
		WithCreatorResolver(resolver)(engine)
	}
	return engine, repo, core, conn
}

type creatorUpdateRepo struct {
	*sourceEngineRepoStub
}

type fixtureCreatorResolver struct{}

func (fixtureCreatorResolver) ResolveAdmin(_ context.Context, tenantID string) (CreatorIdentity, error) {
	return CreatorIdentity{UserID: "user-1", UserName: "User One", TenantID: tenantID}, nil
}

func (r creatorUpdateRepo) UpdateSource(_ context.Context, source store.Source) error {
	r.sources[source.SourceID] = source
	return nil
}

func TestNewSourceUsesQueriedAdminAndKeepsRequesterIdempotency(t *testing.T) {
	for _, userID := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"} {
		t.Run(userID, func(t *testing.T) {
			resolver := &creatorResolverStub{identity: CreatorIdentity{UserID: userID, UserName: "admin", TenantID: "tenant-1"}}
			engine, repo, core, conn := newAdminCreatorEngine(t, resolver)
			req := adminCreatorRequest()
			response, err := engine.CreateSource(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if resolver.calls != 1 || resolver.tenantID != req.TenantID {
				t.Fatalf("creation must query admin in requester tenant, calls=%d tenant=%q", resolver.calls, resolver.tenantID)
			}
			if response.Source.CreatedBy != userID || response.Source.SourceOptions["created_by_name"] != "admin" {
				t.Fatalf("new source must persist queried admin identity: %+v", response.Source)
			}
			if len(core.datasetRequests) != 1 || core.datasetRequests[0].CreatedBy != userID || core.datasetRequests[0].UserName != "admin" {
				t.Fatalf("knowledge base must be owned by queried admin: %+v", core.datasetRequests)
			}
			if core.datasetRequests[0].IdempotencyKey != createDatasetIdempotencyKey(req.CallerID, req.RequestID) {
				t.Fatal("resource idempotency must retain actual requester")
			}
			if len(core.folderRequests) != 1 || core.folderRequests[0].UserID != userID || core.folderRequests[0].UserName != "admin" {
				t.Fatalf("binding folder must be owned by queried admin: %+v", core.folderRequests)
			}
			if len(conn.validateRequests) != 1 || conn.validateRequests[0].UserID != userID || conn.validateRequests[0].ProviderOptions["user_id"] != userID {
				t.Fatalf("connector must validate with source owner: %+v", conn.validateRequests)
			}
			for _, operation := range repo.operations {
				if operation.CallerID != req.CallerID {
					t.Fatalf("operation must retain requester: %+v", operation)
				}
			}
			if req.SourceOptions["created_by_name"] != "spoofed-name" {
				t.Fatal("creation must not mutate caller input")
			}
			resolver.err = errors.New("synthetic auth outage")
			replayed, err := engine.CreateSource(context.Background(), req)
			if err != nil || replayed.Source.SourceID != response.Source.SourceID || resolver.calls != 1 || len(core.datasetRequests) != 1 {
				t.Fatalf("successful replay must not requery identity or create again: %+v err=%v calls=%d", replayed, err, resolver.calls)
			}
		})
	}
}

func TestNewSourceCreatorLookupFailureHasNoCoreSideEffects(t *testing.T) {
	resolver := &creatorResolverStub{err: errors.New("synthetic auth outage")}
	engine, repo, core, conn := newAdminCreatorEngine(t, resolver)
	_, err := engine.CreateSource(context.Background(), adminCreatorRequest())
	if err == nil {
		t.Fatal("unavailable admin identity must fail creation")
	}
	if len(core.datasetRequests) != 0 || len(core.folderRequests) != 0 || len(repo.createRecords) != 0 || len(conn.validateRequests) != 0 {
		t.Fatal("identity failure must happen before creation side effects")
	}
}

func TestNewSourceWithoutCreatorResolverFailsClosed(t *testing.T) {
	engine, _, core, _ := newAdminCreatorEngine(t, nil)
	if _, err := engine.CreateSource(context.Background(), adminCreatorRequest()); err == nil {
		t.Fatal("missing identity resolver must not use requester as creator")
	}
	if len(core.datasetRequests) != 0 {
		t.Fatal("missing resolver must fail before creating a knowledge base")
	}
}

func TestSourceOptionsCannotChangeOrInjectCreatorName(t *testing.T) {
	for _, name := range []string{"", "admin"} {
		t.Run("existing-name-"+name, func(t *testing.T) {
			engine, repo, _, _ := newAdminCreatorEngine(t, &creatorResolverStub{err: errors.New("must not resolve for existing source")})
			options := store.JSON{"source_type": "feishu"}
			if name != "" {
				options["created_by_name"] = name
			}
			repo.sources["existing"] = store.Source{SourceID: "existing", CreatedBy: "legacy-owner", DatasetID: "existing-kb",
				Name: "Existing", Status: SourceStatusActive, ConfigVersion: 1, SourceOptions: options}
			response, err := engine.UpdateSource(context.Background(), "legacy-owner", "existing", UpdateSourceRequest{
				ConfigVersion: 1, SourceOptions: map[string]any{"source_type": "feishu", "created_by_name": "spoofed-name", "setting": "changed"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if response.Source.CreatedBy != "legacy-owner" || response.Source.SourceOptions["setting"] != "changed" {
				t.Fatalf("ordinary options update must keep existing owner: %+v", response.Source)
			}
			got, exists := response.Source.SourceOptions["created_by_name"]
			if (name == "" && exists) || (name != "" && got != name) {
				t.Fatalf("client must not change stored creator policy, existing=%q got=%v exists=%v", name, got, exists)
			}
			response, err = engine.UpdateSource(context.Background(), "legacy-owner", "existing", UpdateSourceRequest{
				ConfigVersion: 2, SourceOptions: map[string]any{"source_type": "feishu", "setting": "changed-again"},
			})
			if err != nil {
				t.Fatal(err)
			}
			got, exists = response.Source.SourceOptions["created_by_name"]
			if (name == "" && exists) || (name != "" && got != name) {
				t.Fatalf("options replacement must retain creator policy, existing=%q got=%v exists=%v", name, got, exists)
			}
		})
	}
}

type creatorOwnedConnector struct {
	*sourceSpyConnector
	ownerID string
}

func (c creatorOwnedConnector) ValidateTarget(ctx context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	if req.UserID != c.ownerID {
		return connector.NormalizedTarget{}, connector.NewError(connector.ErrorCodePermissionDenied, "synthetic connection owner mismatch")
	}
	return c.sourceSpyConnector.ValidateTarget(ctx, req)
}

func TestNewSourceRejectsOtherOwnerConnectionAndCompensatesAsAdmin(t *testing.T) {
	resolver := &creatorResolverStub{identity: CreatorIdentity{UserID: "queried-admin", UserName: "admin", TenantID: "tenant-1"}}
	engine, repo, core, conn := newAdminCreatorEngine(t, resolver)
	registry, err := connector.NewDefaultConnectorRegistry(creatorOwnedConnector{conn, "requester-1"})
	if err != nil {
		t.Fatal(err)
	}
	engine.registry = registry
	_, err = engine.CreateSource(context.Background(), adminCreatorRequest())
	if err == nil || ErrorCodeOf(err) != ErrCodePermissionDenied {
		t.Fatalf("other-user connection must not be used by admin: %v", err)
	}
	if len(repo.createRecords) != 0 || len(core.datasetDeletes) != len(core.datasetRequests) {
		t.Fatal("failed owner validation must not leave a new source or knowledge base")
	}
	for _, deletion := range core.datasetDeletes {
		if deletion.UserID != resolver.identity.UserID {
			t.Fatalf("compensation must act as resource owner: %+v", deletion)
		}
	}
}

type creatorFailingRepo struct {
	creatorUpdateRepo
}

func (creatorFailingRepo) CreateSourceWithBindings(context.Context, store.SourceCreateRecord) error {
	return store.NewStoreError(store.ErrCodeInternal, "synthetic source persistence failure")
}

func TestNewSourceCompensatesCreatedFolderAndKnowledgeBaseAsAdmin(t *testing.T) {
	resolver := &creatorResolverStub{identity: CreatorIdentity{UserID: "queried-admin", UserName: "admin", TenantID: "tenant-1"}}
	engine, repo, core, _ := newAdminCreatorEngine(t, resolver)
	engine.repo = creatorFailingRepo{creatorUpdateRepo{repo}}
	_, err := engine.CreateSource(context.Background(), adminCreatorRequest())
	if err == nil {
		t.Fatal("fixture must fail at source persistence after resource creation")
	}
	if len(core.datasetRequests) != 1 || len(core.folderRequests) != 1 || core.folderRequests[0].Name == "" {
		t.Fatalf("fixture must successfully create a valid named directory before failing: datasets=%+v folders=%+v", core.datasetRequests, core.folderRequests)
	}
	if len(repo.sources) != 0 || len(repo.createRecords) != 0 || len(core.deleteRequests) != 1 || len(core.datasetDeletes) != 1 {
		t.Fatalf("failed persistence must compensate all created resources: sources=%d folder_deletes=%+v dataset_deletes=%+v", len(repo.sources), core.deleteRequests, core.datasetDeletes)
	}
	if core.deleteRequests[0].UserID != resolver.identity.UserID || core.datasetDeletes[0].UserID != resolver.identity.UserID {
		t.Fatalf("both folder and knowledge base compensation must use owner rather than requester: folders=%+v datasets=%+v", core.deleteRequests, core.datasetDeletes)
	}
	if core.deleteRequests[0].DocumentID != core.createdFolders[0] || core.datasetDeletes[0].DatasetID != core.createdDatasets[0] {
		t.Fatal("compensation must delete precisely the resources created by the failed operation")
	}
}

func TestExistingSourceBindingChangesKeepCreatorPolicy(t *testing.T) {
	for _, name := range []string{"admin", ""} {
		for _, operation := range []string{"add", "replace", "source-update-add"} {
			t.Run(name+"/"+operation, func(t *testing.T) {
				resolver := &creatorResolverStub{err: errors.New("existing sources must not resolve a new owner")}
				engine, repo, core, _ := newAdminCreatorEngine(t, resolver)
				options := store.JSON{}
				if name != "" {
					options["created_by_name"] = name
				}
				repo.sources["existing"] = store.Source{SourceID: "existing", DatasetID: "existing-kb", Name: "Existing",
					CreatedBy: "persisted-owner", TenantID: "tenant-1", ConfigVersion: 1, Status: SourceStatusActive, SourceOptions: options}
				input := adminCreatorRequest().Bindings[0]
				var err error
				switch operation {
				case "add":
					_, err = engine.AddBinding(context.Background(), "persisted-owner", "existing", input)
				case "replace":
					repo.bindings["existing"] = []store.Binding{{BindingID: "binding-existing", SourceID: "existing",
						ConnectorType: string(spyConnectorType), TargetType: string(spyTargetType), TargetRef: "old-target",
						TargetFingerprint: "old-target", CoreParentDocumentID: "old-folder", BindingGeneration: 1,
						SyncMode: SyncModeManual, Status: BindingStatusActive}}
					_, err = engine.UpdateBinding(context.Background(), "persisted-owner", "existing", "binding-existing", input)
				case "source-update-add":
					_, err = engine.UpdateSource(context.Background(), "persisted-owner", "existing", UpdateSourceRequest{
						ConfigVersion: 1, BindingsProvided: true, Bindings: []BindingInput{input},
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(core.folderRequests) != 1 || core.folderRequests[0].UserID != "persisted-owner" || core.folderRequests[0].UserName != name || resolver.calls != 0 {
					t.Fatalf("directory changes must use persisted identity: %+v resolver_calls=%d", core.folderRequests, resolver.calls)
				}
			})
		}
	}
}

func TestNewSourceDifferentRequestersDoNotShareCreation(t *testing.T) {
	resolver := &creatorResolverStub{identity: CreatorIdentity{UserID: "queried-admin", UserName: "admin", TenantID: "tenant-1"}}
	engine, _, core, _ := newAdminCreatorEngine(t, resolver)
	first := adminCreatorRequest()
	second := adminCreatorRequest()
	second.CallerID = "requester-2"
	second.Name = "Other_admin-owned_docs"
	for _, req := range []CreateSourceRequest{first, second} {
		if _, err := engine.CreateSource(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if len(core.datasetRequests) != 2 || core.datasetRequests[0].IdempotencyKey == core.datasetRequests[1].IdempotencyKey {
		t.Fatalf("distinct requester operations must not collapse: %+v", core.datasetRequests)
	}
	for _, req := range core.datasetRequests {
		if req.CreatedBy != "queried-admin" || strings.TrimSpace(req.UserName) != "admin" {
			t.Fatalf("all new knowledge bases must use resolved admin: %+v", req)
		}
	}
}
