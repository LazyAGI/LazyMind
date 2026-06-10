package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/config"
	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/localfs"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

func TestBuildSelectsSQLRepositoryAndHTTPAdapters(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Address:                           "127.0.0.1",
		Port:                              18080,
		DBDSN:                             "postgres://scan-control-plane",
		CoreBaseURL:                       "http://core.test",
		DefaultDatasetAlgoID:              "general_algo",
		DefaultDatasetAlgoName:            "General",
		AgentBaseURL:                      "http://agent.test",
		FeishuBaseURL:                     "http://feishu.test",
		AuthServiceBaseURL:                "http://auth.test",
		AuthServiceInternalToken:          "internal-token",
		TempDir:                           t.TempDir(),
		TempTTL:                           24 * time.Hour,
		WorkerLeaseTTL:                    time.Minute,
		WorkerMaxBackoff:                  10 * time.Minute,
		ParseDeadLetterAfter:              3,
		GenerateTasksMaxObjectsPerRequest: 20,
		ParseWorkerGlobalConcurrency:      20,
		ParseWorkerSourceConcurrency:      2,
		WorkerPollInterval:                5 * time.Second,
		CoreResultPollInterval:            10 * time.Second,
		CompensationPollInterval:          30 * time.Second,
	}
	var openedDriver, openedDSN string
	built, err := Build(cfg, WithDBOpener(func(driverName, dsn string) (*sql.DB, error) {
		openedDriver, openedDSN = driverName, dsn
		return nil, nil
	}))
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if openedDriver != "postgres" || openedDSN != cfg.DBDSN {
		t.Fatalf("db opener got driver=%q dsn=%q", openedDriver, openedDSN)
	}
	if built.Repository == nil {
		t.Fatalf("repository is nil, want sql")
	}
	if _, ok := built.CoreClient.(*coreclient.HTTPCoreClient); !ok {
		t.Fatalf("core client = %T, want http", built.CoreClient)
	}
	if built.DefaultDatasetAlgo.AlgoID != "general_algo" || built.DefaultDatasetAlgo.DisplayName != "General" {
		t.Fatalf("default dataset algo not wired: %+v", built.DefaultDatasetAlgo)
	}
	if _, ok := built.AgentClient.(*localfs.HTTPAgentClient); !ok {
		t.Fatalf("agent client = %T, want http", built.AgentClient)
	}
	if _, ok := built.AuthConnectionClient.(*feishu.HTTPAuthConnectionClient); !ok {
		t.Fatalf("auth connection client = %T, want http", built.AuthConnectionClient)
	}
	if _, ok := built.FeishuClient.(*feishu.DefaultFeishuAPIClient); !ok {
		t.Fatalf("feishu client = %T, want http", built.FeishuClient)
	}
	if _, ok := built.TempObjectStore.(*worker.FileTempObjectStore); !ok {
		t.Fatalf("temp store = %T, want file", built.TempObjectStore)
	}
	if _, ok := built.JobQueue.(*taskengine.DBJobQueue); !ok {
		t.Fatalf("job queue = %T, want db", built.JobQueue)
	}
	if built.ParseWorkerRunner == nil || built.Scheduler == nil {
		t.Fatalf("parse worker runner and scheduler should be wired: parse=%v scheduler=%v", built.ParseWorkerRunner, built.Scheduler)
	}
	if built.CrawlWorker == nil || built.CoreResultRunner == nil || built.TempCleanupRunner == nil {
		t.Fatalf("runtime runners should be wired: crawl=%v reconciler=%v temp=%v", built.CrawlWorker, built.CoreResultRunner, built.TempCleanupRunner)
	}
	if got, want := built.ConnectorTypes, []connector.ConnectorType{localfs.ConnectorType, feishu.ConnectorType}; !reflect.DeepEqual(got, want) {
		t.Fatalf("connectors = %+v, want %+v", got, want)
	}
}

func TestBuildRequiresSQLBoundariesBeforeOpeningDB(t *testing.T) {
	t.Parallel()

	dbOpened := false
	_, err := Build(config.Config{
		Address:                           "127.0.0.1",
		Port:                              18080,
		DBDSN:                             "postgres://scan-control-plane",
		CoreBaseURL:                       "://bad-url",
		DefaultDatasetAlgoID:              "general_algo",
		DefaultDatasetAlgoName:            "General",
		AgentBaseURL:                      "http://agent.test",
		FeishuBaseURL:                     "http://feishu.test",
		AuthServiceBaseURL:                "http://auth.test",
		AuthServiceInternalToken:          "internal-token",
		TempDir:                           t.TempDir(),
		TempTTL:                           24 * time.Hour,
		WorkerLeaseTTL:                    time.Minute,
		WorkerMaxBackoff:                  10 * time.Minute,
		ParseDeadLetterAfter:              3,
		GenerateTasksMaxObjectsPerRequest: 20,
		ParseWorkerGlobalConcurrency:      20,
		ParseWorkerSourceConcurrency:      2,
		WorkerPollInterval:                5 * time.Second,
		CoreResultPollInterval:            10 * time.Second,
		CompensationPollInterval:          30 * time.Second,
	}, WithDBOpener(func(string, string) (*sql.DB, error) {
		dbOpened = true
		return nil, errors.New("should not open")
	}))
	if err == nil || dbOpened {
		t.Fatalf("expected adapter config error before db open, err=%v dbOpened=%v", err, dbOpened)
	}
}

func TestBuildMissingRequiredValueReturnsError(t *testing.T) {
	t.Parallel()

	_, err := Build(config.Config{
		Address:                           "127.0.0.1",
		Port:                              18080,
		DBDSN:                             "postgres://scan-control-plane",
		CoreBaseURL:                       "http://core.test",
		DefaultDatasetAlgoID:              "general_algo",
		DefaultDatasetAlgoName:            "General",
		AuthServiceInternalToken:          "internal-token",
		TempDir:                           t.TempDir(),
		TempTTL:                           24 * time.Hour,
		WorkerLeaseTTL:                    time.Minute,
		WorkerMaxBackoff:                  10 * time.Minute,
		ParseDeadLetterAfter:              3,
		GenerateTasksMaxObjectsPerRequest: 20,
		ParseWorkerGlobalConcurrency:      20,
		ParseWorkerSourceConcurrency:      2,
		WorkerPollInterval:                5 * time.Second,
		CoreResultPollInterval:            10 * time.Second,
		CompensationPollInterval:          30 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "agent base url is required") {
		t.Fatalf("expected missing agent url error, got %v", err)
	}
}

func TestEnabledConnectorTypesIncludesLocalFSAndFeishu(t *testing.T) {
	t.Parallel()

	got := enabledConnectorTypes()
	want := []connector.ConnectorType{localfs.ConnectorType, feishu.ConnectorType}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connectors = %+v, want %+v", got, want)
	}
}

func TestConnectorRegistryWiresTempObjectStore(t *testing.T) {
	t.Parallel()

	temp := worker.NewFileTempObjectStore(t.TempDir())
	stagedPath := t.TempDir() + "/local.md"
	if err := os.WriteFile(stagedPath, []byte("local content"), 0o600); err != nil {
		t.Fatalf("write local staged file: %v", err)
	}
	agent := &appLocalAgentStub{contentURI: "file://" + stagedPath}
	registry, err := connectorRegistryFromTypes(
		[]connector.ConnectorType{localfs.ConnectorType, feishu.ConnectorType},
		agent,
		"agent-default",
		"/host/root",
		&appFeishuAuthStub{},
		&appFeishuAPIStub{},
		temp,
	)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	localConn, err := registry.Get(localfs.ConnectorType)
	if err != nil {
		t.Fatalf("get local_fs connector: %v", err)
	}
	localTyped, ok := localConn.(*localfs.LocalFSConnector)
	if !ok {
		t.Fatalf("local_fs connector = %T", localConn)
	}
	localExported, err := localTyped.ExportObject(context.Background(), connector.ExportObjectRequest{
		ObjectKey:     "local_fs:agent-default:path:/host/root/a.md",
		SourceVersion: "1:1",
		ProviderMeta:  connector.ProviderMeta{"agent_id": "agent-default", "path": "/host/root/a.md"},
	})
	if err != nil {
		t.Fatalf("export local_fs through wired registry: %v", err)
	}
	if !strings.HasPrefix(localExported.ContentURI, "scan-temp://") {
		t.Fatalf("local_fs export did not use temp store: %+v", localExported)
	}

	feishuConn, err := registry.Get(feishu.ConnectorType)
	if err != nil {
		t.Fatalf("get feishu connector: %v", err)
	}
	feishuTyped, ok := feishuConn.(*feishu.FeishuConnector)
	if !ok {
		t.Fatalf("feishu connector = %T", feishuConn)
	}
	feishuExported, err := feishuTyped.ExportObject(context.Background(), connector.ExportObjectRequest{
		ObjectKey:     "feishu:drive:file-a",
		SourceVersion: "rev-a",
		ProviderMeta: connector.ProviderMeta{
			"auth_connection_id": "auth-1",
			"kind":               string(feishu.ObjectKindDriveFile),
			"token":              "file-a",
		},
	})
	if err != nil {
		t.Fatalf("export feishu through wired registry: %v", err)
	}
	if !strings.HasPrefix(feishuExported.ContentURI, "scan-temp://") {
		t.Fatalf("feishu export did not use temp store: %+v", feishuExported)
	}
}

func TestRuntimeStartEnqueuesDueSyncRuns(t *testing.T) {
	t.Parallel()

	scheduler := &runtimeSchedulerSpy{calls: make(chan int, 1)}
	runtime := &Runtime{
		workerID:           "test-worker",
		scheduler:          scheduler,
		workerPollInterval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime.Start(ctx)

	select {
	case limit := <-scheduler.calls:
		if limit != runtimeDueSyncRunLimit {
			t.Fatalf("scheduler limit = %d, want %d", limit, runtimeDueSyncRunLimit)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not enqueue due sync runs")
	}
}

type runtimeSchedulerSpy struct {
	calls chan int
}

func (s *runtimeSchedulerSpy) EnqueueDueSyncRuns(_ context.Context, limit int) ([]schedule.SyncRunIntent, error) {
	s.calls <- limit
	return nil, nil
}

type appLocalAgentStub struct {
	contentURI string
}

func (a *appLocalAgentStub) ValidatePath(context.Context, localfs.ValidatePathRequest) (localfs.PathInfo, error) {
	return localfs.PathInfo{}, fmt.Errorf("not implemented")
}

func (a *appLocalAgentStub) ListDir(context.Context, localfs.ListDirRequest) (localfs.ListDirPage, error) {
	return localfs.ListDirPage{}, fmt.Errorf("not implemented")
}

func (a *appLocalAgentStub) StatPath(context.Context, localfs.StatPathRequest) (localfs.PathInfo, error) {
	return localfs.PathInfo{}, fmt.Errorf("not implemented")
}

func (a *appLocalAgentStub) ExportFile(_ context.Context, req localfs.ExportFileRequest) (localfs.ExportedFile, error) {
	if req.ExpectedVersion != "1:1" {
		return localfs.ExportedFile{}, fmt.Errorf("unexpected version %q", req.ExpectedVersion)
	}
	return localfs.ExportedFile{
		ContentURI:    a.contentURI,
		SizeBytes:     1,
		MTimeUnixNano: 1,
		MimeType:      "text/markdown",
		FileExtension: ".md",
	}, nil
}

type appFeishuAuthStub struct{}

func (a *appFeishuAuthStub) GetToken(context.Context, feishu.TokenRequest) (feishu.Token, error) {
	return feishu.Token{AccessToken: "token"}, nil
}

type appFeishuAPIStub struct{}

func (a *appFeishuAPIStub) GetDriveRoot(context.Context, string) (feishu.Object, error) {
	return feishu.Object{}, fmt.Errorf("not implemented")
}

func (a *appFeishuAPIStub) GetDriveFolder(context.Context, string, string) (feishu.Object, error) {
	return feishu.Object{}, fmt.Errorf("not implemented")
}

func (a *appFeishuAPIStub) ListDriveChildren(context.Context, string, string, string, int) (feishu.ObjectPage, error) {
	return feishu.ObjectPage{}, fmt.Errorf("not implemented")
}

func (a *appFeishuAPIStub) DownloadDriveFile(context.Context, string, string, string) (feishu.ExportedContent, error) {
	return feishu.ExportedContent{Content: []byte("drive content"), ExportedVersion: "rev-a"}, nil
}

func (a *appFeishuAPIStub) ExportDriveDocumentMarkdown(context.Context, string, string, string) (feishu.ExportedContent, error) {
	return feishu.ExportedContent{Content: []byte("drive content"), MimeType: "text/markdown", FileExtension: ".md", ExportedVersion: "rev-a"}, nil
}

func (a *appFeishuAPIStub) ListWikiSpaces(context.Context, string, string, int) (feishu.ObjectPage, error) {
	return feishu.ObjectPage{}, fmt.Errorf("not implemented")
}

func (a *appFeishuAPIStub) GetWikiNode(context.Context, string, string, string) (feishu.Object, error) {
	return feishu.Object{}, fmt.Errorf("not implemented")
}

func (a *appFeishuAPIStub) ListWikiChildren(context.Context, string, string, string, string, int) (feishu.ObjectPage, error) {
	return feishu.ObjectPage{}, fmt.Errorf("not implemented")
}

func (a *appFeishuAPIStub) ExportWikiNodeMarkdown(context.Context, string, string, string, string) (feishu.ExportedContent, error) {
	return feishu.ExportedContent{}, fmt.Errorf("not implemented")
}
