package bootstrap

import (
	"net/http"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/capability/internal/coreadapter"
	"lazymind/core/capability/internal/scanadapter"
	mcpadapter "lazymind/core/capability/mcp"
)

type Config struct {
	DB                        *gorm.DB
	LazyDB                    *gorm.DB
	AuthServiceBaseURL        string
	AuthHTTPClient            *http.Client
	KnowledgeSearchBaseURL    string
	InternalServiceToken      string
	KnowledgeSearchHTTPClient *http.Client
	ScanBaseURL               string
	ScanHTTPClient            *http.Client
}

type Runtime struct {
	Service *capability.Service
	MCP     http.Handler
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
		config.ScanHTTPClient,
		0,
	)
	if err != nil {
		return nil, err
	}
	service, err := capability.NewService(capability.Dependencies{
		Skills: skills, Knowledge: knowledge, Documents: documents, Search: search, Cloud: cloud,
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
	return &Runtime{Service: service, MCP: handler}, nil
}
