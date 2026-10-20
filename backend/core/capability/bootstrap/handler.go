package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/core/doc"
	"lazymind/core/knowledgeruntime"
	"lazymind/core/state"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/capability/internal/coreadapter"
	"lazymind/core/capability/internal/scanadapter"
	mcpadapter "lazymind/core/capability/mcp"
	"lazymind/core/externalcapability"
)

type Config struct {
	KnowledgeDecisionRecorded    func(context.Context, knowledgeruntime.Execution, knowledgeruntime.Prepared, string) error
	PendingKnowledgeConfirmation func(context.Context, knowledgeruntime.Execution, knowledgeruntime.Prepared) error
	EnableKnowledgeManagement    bool
	State                        state.Store
	ValidateKnowledgeExecution   knowledgeruntime.ValidateFunc
	ValidateKnowledgeCommit      knowledgeruntime.ValidateCommitFunc
	MoveBudget                   doc.ResourceBudget
	DB                           *gorm.DB
	LazyDB                       *gorm.DB
	AuthServiceBaseURL           string
	AuthHTTPClient               *http.Client
	KnowledgeSearchBaseURL       string
	CloudDocumentBaseURL         string
	InternalServiceToken         string
	KnowledgeSearchHTTPClient    *http.Client
	ScanBaseURL                  string
}

type Runtime struct {
	MCP                     http.Handler
	KnowledgeInternal       http.Handler
	KnowledgeMCP            http.Handler
	KnowledgeDecision       http.HandlerFunc
	KnowledgeUpload         http.Handler
	KnowledgeInternalUpload http.Handler
}

func NewHandler(config Config) (http.Handler, error) {
	runtime, err := NewRuntime(config)
	if err != nil {
		return nil, err
	}
	return runtime.MCP, nil
}

func NewRuntime(config Config) (*Runtime, error) {
	if config.DB == nil {
		return nil, capability.NewError(capability.Internal, "capability.bootstrap", "database is required", false, nil)
	}
	skills, err := coreadapter.NewSkillReaderForDB(config.DB)
	if err != nil {
		return nil, err
	}
	knowledge, err := coreadapter.NewKnowledgeCatalogForDB(config.DB)
	if err != nil {
		return nil, err
	}
	lazyDB := config.LazyDB
	if lazyDB == nil {
		lazyDB = config.DB
	}
	documents, err := coreadapter.NewKnowledgeDocumentReaderForDBs(config.DB, lazyDB)
	if err != nil {
		return nil, err
	}
	search, err := coreadapter.NewKnowledgeSearcherForDB(
		config.DB, config.KnowledgeSearchBaseURL, config.InternalServiceToken, config.KnowledgeSearchHTTPClient,
	)
	if err != nil {
		return nil, err
	}
	cloud, err := scanadapter.NewCloudDocumentReader(
		config.ScanBaseURL,
		config.AuthServiceBaseURL,
		config.InternalServiceToken,
		0,
	)
	if err != nil {
		return nil, err
	}
	vocabularyTrainer, err := coreadapter.NewVocabularyTrainerForDB(config.DB)
	if err != nil {
		return nil, err
	}
	var cloudContent capability.CloudDocumentContentReader
	if strings.TrimSpace(config.CloudDocumentBaseURL) != "" && strings.TrimSpace(config.InternalServiceToken) != "" {
		if err := cloud.SetRuntimeEndpoint(config.CloudDocumentBaseURL); err != nil {
			return nil, err
		}
		cloudContent = cloud
	}
	var management *coreadapter.KnowledgeManagement
	var managementPort capability.KnowledgeManager
	var resources *doc.ResourceService
	if config.EnableKnowledgeManagement {
		originals, e := newCapabilityOriginalService(config.DB)
		if e != nil {
			return nil, e
		}
		resources, e = doc.NewResourceService(doc.ResourceServiceDeps{DB: config.DB, Originals: originals, Budget: config.MoveBudget})
		if e != nil {
			return nil, e
		}
		management = coreadapter.NewKnowledgeManagement(config.DB, lazyDB, resources, originals)
		managementPort = management
	}
	service, err := capability.NewService(capability.Dependencies{
		Management: managementPort, Skills: skills, Knowledge: knowledge, Documents: documents, Search: search, Cloud: cloud, CloudContent: cloudContent, Vocabulary: vocabularyTrainer,
		External: externalcapability.New(config.DB, nil),
	})
	if err != nil {
		return nil, err
	}
	verifier, err := mcpadapter.NewAuthServiceVerifier(mcpadapter.AuthServiceVerifierConfig{
		BaseURL: config.AuthServiceBaseURL, HTTPClient: config.AuthHTTPClient,
	})
	if err != nil {
		return nil, err
	}
	handler, err := mcpadapter.NewHandler(service, mcpadapter.HandlerConfig{Verifier: verifier})
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{MCP: handler}
	if config.EnableKnowledgeManagement {
		kr, e := knowledgeruntime.New(config.State, config.ValidateKnowledgeExecution, func(ctx context.Context, id knowledgeruntime.Identity, name string, args json.RawMessage) (doc.ResourceSnapshot, error) {
			permissions := []string{}
			for p := range id.Principal.Permissions {
				permissions = append(permissions, p)
			}
			op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: id.Principal.UserID, TenantID: id.Principal.TenantID, Permissions: permissions}, AllowedDatasetIDs: id.AllowedDatasetIDs}
			if name == "knowledge.resource.move" {
				var req doc.MoveResourcesRequest
				if e := json.Unmarshal(args, &req); e != nil {
					return doc.ResourceSnapshot{}, e
				}
				return resources.SnapshotMove(ctx, op, req)
			}
			var req doc.DeleteResourcesRequest
			if e := json.Unmarshal(args, &req); e != nil {
				return doc.ResourceSnapshot{}, e
			}
			return resources.Snapshot(ctx, op, req.Targets)
		})
		if e != nil {
			return nil, e
		}
		kr.SetPendingHook(config.PendingKnowledgeConfirmation)
		kr.SetDecisionHook(config.KnowledgeDecisionRecorded)
		kr.SetReceiptLookup(func(ctx context.Context, id knowledgeruntime.Identity, req knowledgeruntime.PrepareRequest) (bool, error) {
			permissions := []string{}
			for permission := range id.Principal.Permissions {
				permissions = append(permissions, permission)
			}
			op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: id.Principal.UserID, TenantID: id.Principal.TenantID, Permissions: permissions}, OperationKey: req.OperationKey}
			switch req.WireName {
			case "knowledge.resource.delete":
				var input doc.DeleteResourcesRequest
				if err := json.Unmarshal(req.Arguments, &input); err != nil {
					return false, err
				}
				_, found, err := resources.ReplayOperation(ctx, op, req.WireName, input)
				return found, err
			case "knowledge.resource.move":
				var input doc.MoveResourcesRequest
				if err := json.Unmarshal(req.Arguments, &input); err != nil {
					return false, err
				}
				return resources.HasCommittedMove(ctx, op, input)
			}
			return false, nil
		})
		management.SetBeforeCommit(kr.ValidateCommitPreparation)
		management.SetCommitGuard(func(ctx context.Context, tx *gorm.DB, c capability.InvocationContext) error {
			return kr.GuardCommit(ctx, tx, c.ExecutionKey, config.ValidateKnowledgeCommit)
		})
		internalVerifier := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
			id, execution, e := kr.Authenticate(ctx, token)
			if e != nil {
				return nil, e
			}
			scopes := []string{}
			for p := range id.Principal.Permissions {
				scopes = append(scopes, p)
			}
			return &auth.TokenInfo{UserID: id.Principal.UserID, Scopes: scopes, Extra: map[string]any{"tenant_id": id.Principal.TenantID, "execution": execution}}, nil
		}
		runtime.KnowledgeMCP, e = mcpadapter.NewHandler(service, mcpadapter.HandlerConfig{Verifier: internalVerifier, ManagementOnly: true, AuthorizeCall: func(ctx context.Context, req *mcp.CallToolRequest) (capability.InvocationContext, error) {
			if req.Extra == nil || req.Extra.TokenInfo == nil || req.Params == nil {
				return capability.InvocationContext{}, errors.New("authenticated execution missing")
			}
			execution, ok := req.Extra.TokenInfo.Extra["execution"].(knowledgeruntime.Execution)
			if !ok {
				return capability.InvocationContext{}, errors.New("execution missing")
			}
			return kr.AuthorizeDispatch(ctx, execution, req.Params.Name, req.Params.Arguments, req.Params.GetMeta())
		}})
		if e != nil {
			return nil, e
		}
		runtime.KnowledgeInternalUpload = kr.OriginalUpload(config.InternalServiceToken, doc.UploadOriginalHTTP)
		runtime.KnowledgeUpload = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimSpace(r.Header.Get("Authorization"))
			if !strings.HasPrefix(raw, "Bearer ") {
				http.Error(w, "unauthorized", 401)
				return
			}
			info, e := verifier(r.Context(), strings.TrimPrefix(raw, "Bearer "), r)
			if e != nil || info == nil {
				http.Error(w, "unauthorized", 401)
				return
			}
			allowed := false
			for _, p := range info.Scopes {
				if p == "document.write" || p == "*" {
					allowed = true
				}
			}
			if !allowed {
				http.Error(w, "forbidden", 403)
				return
			}
			clone := r.Clone(r.Context())
			clone.Header = clone.Header.Clone()
			clone.Header.Set("X-User-Id", info.UserID)
			tenant, _ := info.Extra["tenant_id"].(string)
			clone.Header.Set("X-Tenant-Id", tenant)
			doc.UploadOriginalHTTP(w, clone)
		})
		runtime.KnowledgeInternal = kr.InternalHandler(config.InternalServiceToken)
		runtime.KnowledgeDecision = kr.Decision
	}
	return runtime, nil
}
