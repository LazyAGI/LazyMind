package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	algorithmHealthTimeout = 15 * time.Minute
)

type AlgorithmServiceSpec struct {
	Name       string
	Module     []string
	Port       int
	HealthPath string
}

type AlgorithmServiceManager struct {
	runner CommandRunner
}

func NewAlgorithmServiceManager(r CommandRunner) *AlgorithmServiceManager {
	return &AlgorithmServiceManager{runner: r}
}

func algorithmProcessSpecs(cfg AlgorithmConfig) []AlgorithmServiceSpec {
	specs := []AlgorithmServiceSpec{
		{Name: processorServerProcessName, Module: []string{"-m", "lazymind.processor.service.server"}, Port: cfg.ProcessorPort, HealthPath: "/health"},
		{Name: processorWorkerProcessName, Module: []string{"-m", "lazymind.processor.service.worker"}, Port: cfg.WorkerPort, HealthPath: "/health"},
		{Name: algoProcessName, Module: []string{"-m", "lazymind.parsing.app"}, Port: cfg.AlgoPort, HealthPath: "/docs"},
		{Name: docServerProcessName, Module: []string{filepath.Join("backend", "core", "doc", "doc_server.py"), "--port", strconv.Itoa(cfg.DocPort), "--parser-url", fmt.Sprintf("http://127.0.0.1:%d", cfg.ProcessorPort)}, Port: cfg.DocPort, HealthPath: "/v1/health"},
		{Name: chatProcessName, Module: []string{"-m", "lazymind.router.app", "--host", "0.0.0.0", "--port", strconv.Itoa(cfg.ChatPort)}, Port: cfg.ChatPort, HealthPath: "/health"},
	}
	if cfg.EnableEvo {
		specs = append(specs, AlgorithmServiceSpec{
			Name:       evoProcessName,
			Module:     []string{"-m", "uvicorn", "evo.service.api:get_app", "--factory", "--host", "127.0.0.1", "--port", strconv.Itoa(cfg.EvoPort)},
			Port:       cfg.EvoPort,
			HealthPath: "/healthz",
		})
	}
	return specs
}

func algorithmSpecByName(cfg AlgorithmConfig, name string) (AlgorithmServiceSpec, bool) {
	for _, spec := range algorithmProcessSpecs(cfg) {
		if spec.Name == name {
			return spec, true
		}
	}
	return AlgorithmServiceSpec{}, false
}

func algorithmLogPath(paths RuntimePaths, service string) string {
	switch service {
	case docServerProcessName:
		return paths.DocServerLog
	case processorServerProcessName:
		return paths.ProcessorServerLog
	case processorWorkerProcessName:
		return paths.ProcessorWorkerLog
	case algoProcessName:
		return paths.AlgoLog
	case chatProcessName:
		return paths.ChatLog
	case evoProcessName:
		return paths.EvoLog
	default:
		return filepath.Join(paths.LogsDir, service+".log")
	}
}

func (m *AlgorithmServiceManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, service string) error {
	spec, ok := algorithmSpecByName(cfg.Algorithm, service)
	if !ok {
		return fmt.Errorf("unknown algorithm service: %s", service)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := ensureAlgorithmDataDirs(paths); err != nil {
		return err
	}
	if err := m.preparePython(ctx, paths, cfg.Algorithm.EnableEvo); err != nil {
		return err
	}
	if err := m.waitForDependencies(ctx, cfg, spec.Name); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, paths.AlgorithmPython, spec.Module...)
	cmd.Dir = paths.RepoRoot
	cmd.Env = append(os.Environ(), algorithmServiceEnv(cfg, paths, spec.Name)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s failed: %w", service, err)
	}
	pidFile := algorithmPIDFile(paths, service)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		return err
	}
	registerLocalProcess(paths, service, cmd.Process.Pid, []int{spec.Port}, append([]string{paths.AlgorithmPython}, spec.Module...))

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	if err := waitForHTTPHealth(ctx, spec.Port, spec.HealthPath, service, algorithmHealthTimeout, waitErr); err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		_ = os.Remove(pidFile)
		unregisterLocalProcess(paths, service, cmd.Process.Pid)
		return err
	}
	if service == algoProcessName {
		if err := waitForAlgorithmRegistration(ctx, cfg.Algorithm.ProcessorPort, algorithmHealthTimeout); err != nil {
			_ = killAlgorithmProcess(cmd.Process)
			_ = os.Remove(pidFile)
			unregisterLocalProcess(paths, service, cmd.Process.Pid)
			return err
		}
	}

	err := <-waitErr
	_ = os.Remove(pidFile)
	unregisterLocalProcess(paths, service, cmd.Process.Pid)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s exited: %w", service, err)
	}
	return nil
}

func (m *AlgorithmServiceManager) Down(ctx context.Context, paths RuntimePaths, service string) error {
	pidFile := algorithmPIDFile(paths, service)
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidFile)
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidFile)
		return nil
	}
	if err := signalProcessGroup(pid, syscall.SIGINT); err != nil {
		_ = proc.Signal(os.Interrupt)
	}
	if !processAlive(pid) {
		_ = os.Remove(pidFile)
		return nil
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = signalProcessGroup(pid, syscall.SIGKILL)
			_ = proc.Kill()
			return ctx.Err()
		case <-deadline.C:
			_ = signalProcessGroup(pid, syscall.SIGKILL)
			_ = proc.Kill()
			_ = os.Remove(pidFile)
			return nil
		case <-ticker.C:
			if !processAlive(pid) {
				_ = os.Remove(pidFile)
				return nil
			}
		}
	}
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, signal)
}

func killAlgorithmProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	_ = signalProcessGroup(proc.Pid, syscall.SIGKILL)
	return proc.Kill()
}

func (m *AlgorithmServiceManager) preparePython(ctx context.Context, paths RuntimePaths, includeEvo bool) error {
	if err := ensureLazyLLMSubmodule(ctx, m.runner, paths.RepoRoot); err != nil {
		return err
	}
	release, err := acquireAlgorithmPythonLock(ctx, paths)
	if err != nil {
		return err
	}
	defer release()
	stamp, err := algorithmReadyStamp(paths, includeEvo)
	if err != nil {
		return err
	}
	if _, err := os.Stat(stamp); err == nil {
		if m.pythonModuleAvailable(ctx, paths, "pkg_resources") {
			return nil
		}
	}
	if _, err := os.Stat(paths.AlgorithmPython); os.IsNotExist(err) {
		if err := m.createVenv(ctx, paths, false); err != nil {
			return err
		}
	}
	if err := m.installAlgorithmPythonDeps(ctx, paths, includeEvo); err != nil {
		if rebuildErr := m.createVenv(ctx, paths, true); rebuildErr != nil {
			return fmt.Errorf("prepare algorithm python failed and venv rebuild failed: %w (original install error: %v)", rebuildErr, err)
		}
		if retryErr := m.installAlgorithmPythonDeps(ctx, paths, includeEvo); retryErr != nil {
			return fmt.Errorf("prepare algorithm python failed after venv rebuild: %w (original install error: %v)", retryErr, err)
		}
	}
	return os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

func (m *AlgorithmServiceManager) installAlgorithmPythonDeps(ctx context.Context, paths RuntimePaths, includeEvo bool) error {
	lazyllm := filepath.Join(paths.AlgorithmVenv, "bin", "lazyllm")
	uv, ok := uvCommand()
	if !ok {
		return fmt.Errorf("uv is required to install algorithm requirements; install uv or set %s", authServiceUVEnvVar)
	}
	installSteps := []Command{
		{Name: uv, Args: localPythonPipInstallArgs(paths.AlgorithmPython, "setuptools<81"), Dir: paths.RepoRoot, Env: pythonRuntimeEnv(paths)},
		{Name: uv, Args: localPythonPipInstallArgs(paths.AlgorithmPython, "lazyllm"), Dir: paths.RepoRoot, Env: pythonRuntimeEnv(paths)},
		{Name: lazyllm, Args: []string{"install", "rag"}, Dir: paths.RepoRoot, Env: pythonDependencyCacheEnv(paths)},
		{Name: uv, Args: localPythonPipInstallArgs(paths.AlgorithmPython, "-r", filepath.Join(paths.RepoRoot, "algorithm", "requirements.txt")), Dir: paths.RepoRoot, Env: pythonRuntimeEnv(paths)},
	}
	if includeEvo {
		installSteps = append(installSteps, Command{Name: uv, Args: localPythonPipInstallArgs(paths.AlgorithmPython, "-r", filepath.Join(paths.RepoRoot, "evo", "requirements.txt")), Dir: paths.RepoRoot, Env: pythonRuntimeEnv(paths)})
	}
	for _, step := range installSteps {
		res, err := m.runner.Run(ctx, step)
		if err != nil {
			return fmt.Errorf("prepare algorithm python failed at %s %s: %w (%s)", step.Name, strings.Join(step.Args, " "), err, strings.TrimSpace(res.Stderr))
		}
	}
	return nil
}

func algorithmReadyStamp(paths RuntimePaths, includeEvo bool) (string, error) {
	hash := sha256.New()
	files := []string{filepath.Join(paths.RepoRoot, "algorithm", "requirements.txt")}
	prefix := "algorithm"
	if includeEvo {
		files = append(files, filepath.Join(paths.RepoRoot, "evo", "requirements.txt"))
		prefix = "algorithm-evo"
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(path)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	digest := hex.EncodeToString(hash.Sum(nil))[:16]
	return filepath.Join(paths.PythonStateDir, prefix+"-"+digest+".ready"), nil
}

func (m *AlgorithmServiceManager) pythonModuleAvailable(ctx context.Context, paths RuntimePaths, module string) bool {
	if _, err := os.Stat(paths.AlgorithmPython); err != nil {
		return false
	}
	_, err := m.runner.Run(ctx, Command{Name: paths.AlgorithmPython, Args: []string{"-c", "import " + module}, Dir: paths.RepoRoot})
	return err == nil
}

func (m *AlgorithmServiceManager) createVenv(ctx context.Context, paths RuntimePaths, clear bool) error {
	python, err := ensureLocalPythonRuntime(ctx, m.runner, paths, envText(localPythonVersionEnvVar, defaultLocalPythonVersion))
	if err != nil {
		return err
	}
	uv, ok := uvCommand()
	if !ok {
		return fmt.Errorf("uv is required to create algorithm venv; install uv or set %s", authServiceUVEnvVar)
	}
	if res, err := m.runner.Run(ctx, Command{Name: uv, Args: localPythonVenvArgs(python, clear, paths.AlgorithmVenv), Dir: paths.RepoRoot, Env: pythonRuntimeEnv(paths)}); err != nil {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("create algorithm venv with uv failed: %w (%s)", err, detail)
	}
	return nil
}

func acquireAlgorithmPythonLock(ctx context.Context, paths RuntimePaths) (func(), error) {
	lockFile := filepath.Join(paths.RunDir, "algorithm-python.lock")
	for {
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(lockFile) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		alive, readErr := upLockProcessAlive(lockFile)
		if readErr == nil && !alive {
			_ = os.Remove(lockFile)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (m *AlgorithmServiceManager) waitForDependencies(ctx context.Context, cfg RuntimeConfig, service string) error {
	switch service {
	case processorServerProcessName:
		return nil
	case processorWorkerProcessName:
		return waitForHTTPOnly(ctx, cfg.Algorithm.ProcessorPort, "/health", "processor-server", 3*time.Minute)
	case algoProcessName:
		if err := waitForHTTPOnly(ctx, cfg.Algorithm.ProcessorPort, "/health", "processor-server", 3*time.Minute); err != nil {
			return err
		}
		if cfg.ModeProfile.VectorStore.ManagedProcess {
			if err := waitForTCP(ctx, "127.0.0.1", cfg.ModeProfile.VectorStore.Port, "Milvus", 5*time.Minute); err != nil {
				return err
			}
		}
		if localSegmentStoreUsesBuiltInOpenSearch() {
			if err := waitForTCP(ctx, "127.0.0.1", cfg.Algorithm.OpenSearchPort, "OpenSearch", 5*time.Minute); err != nil {
				return err
			}
		}
	case docServerProcessName:
		return waitForHTTPOnly(ctx, cfg.Algorithm.ProcessorPort, "/health", "processor-server", 3*time.Minute)
	case chatProcessName:
		if err := waitForHTTPOnly(ctx, cfg.Algorithm.AlgoPort, "/docs", "lazyllm-algo", 5*time.Minute); err != nil {
			return err
		}
		if err := waitForAlgorithmRegistration(ctx, cfg.Algorithm.ProcessorPort, 5*time.Minute); err != nil {
			return err
		}
		return waitForHTTPOnly(ctx, cfg.LocalProxy.CoreHostPort, "/health", "core", 5*time.Minute)
	case evoProcessName:
		return waitForHTTPOnly(ctx, cfg.Algorithm.ChatPort, "/health", "chat", 5*time.Minute)
	}
	return nil
}

func localSegmentStorePath(paths RuntimePaths) string {
	return filepath.Join(paths.AlgorithmHome, "sqlite", "segment-store.db")
}

func localSegmentStoreURIOrPath(cfg RuntimeConfig, paths RuntimePaths) string {
	if strings.EqualFold(localSegmentStoreType(), "opensearch") {
		return envText("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", fmt.Sprintf("https://127.0.0.1:%d", cfg.Algorithm.OpenSearchPort))
	}
	return envText("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", localSegmentStorePath(paths))
}

func ensureAlgorithmDataDirs(paths RuntimePaths) error {
	dirs := []string{
		paths.UploadRoot,
		paths.LazyLLMTempDir,
		paths.OCRCacheDir,
		paths.TracesDir,
		paths.SubagentDataDir,
		paths.LazyLLMHome,
		paths.EvoDataDir,
		filepath.Join(paths.AlgorithmHome, "agent_workspace"),
		filepath.Join(paths.AlgorithmHome, "sqlite"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func ensureLazyLLMSubmodule(ctx context.Context, runner CommandRunner, repoRoot string) error {
	required := filepath.Join(repoRoot, "algorithm", "lazyllm", "lazyllm")
	if info, err := os.Stat(required); err == nil && info.IsDir() {
		return nil
	}
	res, err := runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"submodule", "update", "--init", "algorithm/lazyllm"},
		Dir:  repoRoot,
	})
	if err != nil {
		return fmt.Errorf("initialize algorithm/lazyllm submodule failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	if info, err := os.Stat(required); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("algorithm/lazyllm submodule is still not checked out after git submodule update --init algorithm/lazyllm")
}

func algorithmServiceEnv(cfg RuntimeConfig, paths RuntimePaths, service string) []string {
	pythonPath := strings.Join([]string{
		filepath.Join(paths.RepoRoot, "algorithm", "lazyllm"),
		filepath.Join(paths.RepoRoot, "algorithm"),
		paths.RepoRoot,
	}, string(os.PathListSeparator))
	lazyLLMDBURL := sqliteURL(paths.LazyLLMDBPath)
	coreDBURL := sqliteURL(paths.CoreDBPath)
	noProxy := envText("no_proxy", "127.0.0.1,localhost,::1,core,chat,evo-api,doc-server,lazyllm-algo,parsing,milvus,opensearch,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16")
	noProxyUpper := envText("NO_PROXY", noProxy)
	routerPoolStart, routerPoolEnd := localRouterPortPool(cfg)
	env := []string{
		"LAZYMIND_RUNTIME_MODE=local",
		"PYTHONPATH=" + pythonPath,
		"LAZYMIND_HOME=" + paths.AlgorithmHome,
		"LAZYLLM_HOME=" + paths.LazyLLMHome,
		"LAZYMIND_DATABASE_URL=" + lazyLLMDBURL,
		"LAZYMIND_CORE_DATABASE_URL=" + coreDBURL,
		"LAZYMIND_ACL_DB_DSN=" + coreDBURL,
		"LAZYMIND_SHARED_UPLOAD_DIR=" + paths.UploadRoot,
		"LAZYMIND_UPLOAD_DIR=" + paths.UploadRoot,
		"LAZYMIND_UPLOAD_ROOT=" + paths.UploadRoot,
		"LAZYMIND_DOCUMENT_SERVICE_STORAGE_DIR=" + paths.UploadRoot,
		"http_proxy=" + envText("http_proxy", ""),
		"https_proxy=" + envText("https_proxy", ""),
		"HTTP_PROXY=" + envText("HTTP_PROXY", ""),
		"HTTPS_PROXY=" + envText("HTTPS_PROXY", ""),
		"no_proxy=" + noProxy,
		"NO_PROXY=" + noProxyUpper,
		"LAZYLLM_OPENAI_API_KEY=" + envText("LAZYLLM_OPENAI_API_KEY", ""),
		"LAZYLLM_GLM_API_KEY=" + envText("LAZYLLM_GLM_API_KEY", ""),
		"LAZYLLM_QWEN_API_KEY=" + envText("LAZYLLM_QWEN_API_KEY", ""),
		"LAZYLLM_SENSENOVA_API_KEY=" + envText("LAZYLLM_SENSENOVA_API_KEY", ""),
		"LAZYLLM_SENSENOVA_SECRET_KEY=" + envText("LAZYLLM_SENSENOVA_SECRET_KEY", ""),
		"LAZYLLM_KIMI_API_KEY=" + envText("LAZYLLM_KIMI_API_KEY", ""),
		"LAZYLLM_DEEPSEEK_API_KEY=" + envText("LAZYLLM_DEEPSEEK_API_KEY", ""),
		"LAZYLLM_DOUBAO_API_KEY=" + envText("LAZYLLM_DOUBAO_API_KEY", ""),
		"LAZYLLM_SILICONFLOW_API_KEY=" + envText("LAZYLLM_SILICONFLOW_API_KEY", ""),
		"LAZYLLM_MINIMAX_API_KEY=" + envText("LAZYLLM_MINIMAX_API_KEY", ""),
		"LAZYLLM_AIPING_API_KEY=" + envText("LAZYLLM_AIPING_API_KEY", ""),
		"LAZYMIND_MAAS_API_KEY=" + envText("LAZYMIND_MAAS_API_KEY", ""),
		"LAZYLLM_TEMP_DIR=" + paths.LazyLLMTempDir,
		"LAZYMIND_OCR_CACHE_DIR=" + paths.OCRCacheDir,
		"LAZYMIND_MOUNT_BASE_DIR=" + paths.UploadRoot,
		"TZ=" + envText("TZ", "Asia/Shanghai"),
		"LANGFUSE_HOST=" + envText("LANGFUSE_HOST", ""),
		"LANGFUSE_BASE_URL=" + envText("LANGFUSE_BASE_URL", ""),
		"LANGFUSE_PUBLIC_KEY=" + envText("LANGFUSE_PUBLIC_KEY", ""),
		"LANGFUSE_SECRET_KEY=" + envText("LANGFUSE_SECRET_KEY", ""),
		"LAZYLLM_TRACE_ENABLED=" + envText("LAZYLLM_TRACE_ENABLED", "1"),
		"LAZYLLM_TRACE_LOCAL_STORAGE_DIR=" + paths.TracesDir,
		"LAZYLLM_TRACE_CONSUME_BACKEND=local",
		"LAZYLLM_TRACE_BACKEND=local",
		"OTEL_EXPORTER_OTLP_TIMEOUT=" + envText("OTEL_EXPORTER_OTLP_TIMEOUT", "60"),
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT=" + envText("OTEL_EXPORTER_OTLP_TRACES_TIMEOUT", "60"),
		"LAZYMIND_LANGFUSE_FORCE_FLUSH_TIMEOUT_MS=" + envText("LAZYMIND_LANGFUSE_FORCE_FLUSH_TIMEOUT_MS", "70000"),
		"LAZYMIND_OCR_SERVER_URL=" + envText("LAZYMIND_OCR_SERVER_URL", ""),
		"LAZYMIND_MINERU_BACKEND=" + envText("LAZYMIND_MINERU_BACKEND", "pipeline"),
		"LAZYMIND_MINERU_SERVER_PORT=" + envText("LAZYMIND_MINERU_SERVER_PORT", "8000"),
		"LAZYLLM_MINERU_BACKEND=" + envText("LAZYLLM_MINERU_BACKEND", envText("LAZYMIND_MINERU_BACKEND", "pipeline")),
		"LAZYLLM_MINERU_API_KEY=" + envText("LAZYLLM_MINERU_API_KEY", ""),
		"LAZYLLM_PADDLE_API_KEY=" + envText("LAZYLLM_PADDLE_API_KEY", ""),
		"LAZYLLM_INIT_DOC=True",
		"LAZYLLM_EXPECTED_LOG_MODULES=all",
		"LAZYMIND_MODEL_CONFIG_PATH=" + envText("LAZYMIND_MODEL_CONFIG_PATH", "dynamic"),
		"LAZYMIND_DOCUMENT_PROCESSOR_URL=" + fmt.Sprintf("http://127.0.0.1:%d", cfg.Algorithm.ProcessorPort),
		"LAZYMIND_DOCUMENT_PROCESSOR_PORT=" + strconv.Itoa(cfg.Algorithm.ProcessorPort),
		"LAZYMIND_DOCUMENT_WORKER_PORT=" + strconv.Itoa(cfg.Algorithm.WorkerPort),
		"LAZYMIND_DOCUMENT_WORKER_NUM_WORKERS=" + envText("LAZYMIND_DOCUMENT_WORKER_NUM_WORKERS", "1"),
		"LAZYMIND_DOCUMENT_WORKER_LEASE_DURATION=" + envText("LAZYMIND_DOCUMENT_WORKER_LEASE_DURATION", "300"),
		"LAZYMIND_DOCUMENT_WORKER_LEASE_RENEW_INTERVAL=" + envText("LAZYMIND_DOCUMENT_WORKER_LEASE_RENEW_INTERVAL", "60"),
		"LAZYMIND_DOCUMENT_WORKER_HIGH_PRIORITY_TASK_TYPES=" + envText("LAZYMIND_DOCUMENT_WORKER_HIGH_PRIORITY_TASK_TYPES", ""),
		"LAZYMIND_DOCUMENT_WORKER_HIGH_PRIORITY_ONLY=" + envText("LAZYMIND_DOCUMENT_WORKER_HIGH_PRIORITY_ONLY", "false"),
		"LAZYMIND_DOCUMENT_WORKER_POLL_MODE=" + envText("LAZYMIND_DOCUMENT_WORKER_POLL_MODE", "direct"),
		"LAZYMIND_DOCUMENT_SERVICE_PORT=" + strconv.Itoa(cfg.Algorithm.DocPort),
		"LAZYMIND_ALGO_SERVER_PORT=" + strconv.Itoa(cfg.Algorithm.AlgoPort),
		"LAZYLLM_ALGO_REGISTER_POLICY=" + envText("LAZYLLM_ALGO_REGISTER_POLICY", "force"),
		"LAZYMIND_USE_INNER_MODEL=true",
		"LAZYMIND_RESET_ALGO_ON_STARTUP=" + envText("LAZYMIND_RESET_ALGO_ON_STARTUP", "false"),
		"LAZYMIND_RESET_ALL_ON_STARTUP=" + envText("LAZYMIND_RESET_ALL_ON_STARTUP", "false"),
		"LAZYMIND_MILVUS_URI=" + cfg.ModeProfile.VectorStore.Endpoint,
		"LAZYMIND_OPENSEARCH_URI=" + envText("LAZYMIND_OPENSEARCH_URI", fmt.Sprintf("https://127.0.0.1:%d", cfg.Algorithm.OpenSearchPort)),
		"LAZYMIND_OPENSEARCH_USER=" + envText("LAZYMIND_OPENSEARCH_USER", "admin"),
		"LAZYMIND_OPENSEARCH_PASSWORD=" + envText("LAZYMIND_OPENSEARCH_PASSWORD", "LazyRAG_OpenSearch123!"),
		"LAZYMIND_SEGMENT_STORE_TYPE=" + localSegmentStoreType(),
		"LAZYMIND_SEGMENT_STORE_URI_OR_PATH=" + localSegmentStoreURIOrPath(cfg, paths),
		"LAZYMIND_SEGMENT_STORE_USER=" + envText("LAZYMIND_SEGMENT_STORE_USER", "admin"),
		"LAZYMIND_SEGMENT_STORE_PASSWORD=" + envText("LAZYMIND_SEGMENT_STORE_PASSWORD", "LazyRAG_OpenSearch123!"),
		"LAZYMIND_DOCUMENT_SERVER_URL=" + fmt.Sprintf("http://127.0.0.1:%d,general_algo", cfg.Algorithm.AlgoPort),
		"LAZYMIND_AGENTIC_KB_URL=" + fmt.Sprintf("http://127.0.0.1:%d", cfg.Algorithm.AlgoPort),
		"LAZYMIND_DEFAULT_CHAT_DATASET=algo",
		"LAZYMIND_CORE_API_URL=" + fmt.Sprintf("http://127.0.0.1:%d", cfg.LocalProxy.CoreHostPort),
		"LAZYMIND_CORE_SERVICE_URL=" + fmt.Sprintf("http://127.0.0.1:%d", cfg.LocalProxy.CoreHostPort),
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=" + envText("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "dev-internal-service-token"),
		"LAZYMIND_FILE_URL_SIGN_SECRET=" + envText("LAZYMIND_FILE_URL_SIGN_SECRET", "changeme-in-production"),
		"LAZYMIND_FILE_URL_EXPIRE_SECONDS=" + envText("LAZYMIND_FILE_URL_EXPIRE_SECONDS", "3600"),
		"LAZYMIND_MAX_RETRIES=" + envText("LAZYMIND_MAX_RETRIES", "20"),
		"LAZYMIND_REVIEW_MAX_RETRIES=" + envText("LAZYMIND_REVIEW_MAX_RETRIES", "5"),
		"LAZYMIND_SKILL_REVIEW_DEBUG=" + envText("LAZYMIND_SKILL_REVIEW_DEBUG", "false"),
		"LAZYMIND_MAX_CONCURRENCY=" + envText("LAZYMIND_MAX_CONCURRENCY", "10"),
		"LAZYMIND_LLM_PRIORITY=" + envText("LAZYMIND_LLM_PRIORITY", "0"),
		"LAZYMIND_ENABLE_ROUTER=" + envText("LAZYMIND_ENABLE_ROUTER", "true"),
		"LAZYMIND_ROUTER_HOST=" + envText("LAZYMIND_ROUTER_HOST", "127.0.0.1"),
		routerPortPoolStartEnvVar + "=" + strconv.Itoa(routerPoolStart),
		routerPortPoolEndEnvVar + "=" + strconv.Itoa(routerPoolEnd),
		routerPortsPerInstanceEnvVar + "=" + strconv.Itoa(defaultRouterPortsPerInstance),
		"LAZYMIND_ROUTER_DEFAULT_ALGO_PATH=" + filepath.Join(paths.RepoRoot, "algorithm", "lazymind", "chat"),
		"LAZYMIND_ROUTER_DEFAULT_INSTANCE_COUNT=1",
		"LAZYMIND_PLUGINS_DIR=" + filepath.Join(paths.RepoRoot, "plugins"),
		"LAZYMIND_AGENTIC_WORKSPACE=" + filepath.Join(paths.AlgorithmHome, "agent_workspace"),
		"LAZYMIND_SUBAGENT_WORKSPACE=" + paths.SubagentDataDir,
		"LAZYMIND_EVO_API_PORT=" + strconv.Itoa(cfg.Algorithm.EvoPort),
		"LAZYMIND_EVO_BASE_DIR=" + paths.EvoDataDir,
		"LAZYMIND_EVO_CHAT_SOURCE=" + filepath.Join(paths.RepoRoot, "algorithm", "lazymind", "chat"),
		"LAZYMIND_EVO_CODE_TIMEOUT_S=" + envText("LAZYMIND_EVO_CODE_TIMEOUT_S", "900"),
		"LAZYMIND_EVO_LLM_ROLE=" + envText("LAZYMIND_EVO_LLM_ROLE", "evo_llm"),
		"LAZYMIND_EVO_KB_BASE_URL=" + fmt.Sprintf("http://127.0.0.1:%d", cfg.Algorithm.DocPort),
		"LAZYMIND_EVO_CHUNK_BASE_URL=" + fmt.Sprintf("http://127.0.0.1:%d", cfg.Algorithm.DocPort),
		"LAZYMIND_EVO_ROUTER_CHAT_URL=" + fmt.Sprintf("http://127.0.0.1:%d/api/chat/stream", cfg.Algorithm.ChatPort),
		"LAZYMIND_WORD_GROUP_APPLY_URL=" + envText("LAZYMIND_WORD_GROUP_APPLY_URL", ""),
	}
	if service == docServerProcessName {
		env = append(env, "LAZYMIND_DOCUMENT_SERVICE_CALLBACK_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.DocPort)+"/v1/internal/callbacks/tasks")
	}
	return env
}

func localRouterPortPool(cfg RuntimeConfig) (int, int) {
	if cfg.Algorithm.RouterPortPoolStart > 0 {
		end := cfg.Algorithm.RouterPortPoolEnd
		if end < cfg.Algorithm.RouterPortPoolStart {
			end = cfg.Algorithm.RouterPortPoolStart + defaultRouterPortsPerInstance - 1
		}
		return cfg.Algorithm.RouterPortPoolStart, end
	}
	if strings.TrimSpace(os.Getenv(routerPortPoolStartEnvVar)) != "" {
		start := envPort(routerPortPoolStartEnvVar, defaultRouterPortPoolStart)
		end := envPort(routerPortPoolEndEnvVar, start+defaultRouterPortsPerInstance-1)
		return start, end
	}
	offset := cfg.ProcessComposePort - defaultProcessComposePort
	start := defaultRouterPortPoolStart + offset*defaultRouterPortsPerInstance
	if start < 1024 || start+defaultRouterPortsPerInstance-1 >= 65536 {
		start = defaultRouterPortPoolStart
	}
	end := start + defaultRouterPortsPerInstance - 1
	if strings.TrimSpace(os.Getenv(routerPortPoolEndEnvVar)) != "" {
		end = envPort(routerPortPoolEndEnvVar, end)
	}
	return start, end
}

func algorithmPIDFile(paths RuntimePaths, service string) string {
	return filepath.Join(paths.AlgorithmPIDDir, service+".pid")
}

func waitForHostAlgorithmReadiness(ctx context.Context, cfg RuntimeConfig) error {
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		if err := waitForHTTPOnly(ctx, spec.Port, spec.HealthPath, spec.Name, algorithmHealthTimeout); err != nil {
			return err
		}
	}
	return waitForAlgorithmRegistration(ctx, cfg.Algorithm.ProcessorPort, algorithmHealthTimeout)
}

func waitForAlgorithmRegistration(ctx context.Context, processorPort int, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	url := fmt.Sprintf("http://127.0.0.1:%d/algo/list", processorPort)
	for {
		if algorithmRegistered(ctx, url) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for algorithm registration at %s", url)
		case <-ticker.C:
		}
	}
}

func algorithmRegistered(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var payload struct {
		Data []struct {
			AlgoID string `json:"algo_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false
	}
	for _, item := range payload.Data {
		if item.AlgoID == "general_algo" {
			return true
		}
	}
	return false
}

func waitForHTTPOnly(ctx context.Context, port int, path string, label string, timeout time.Duration) error {
	return waitForHTTPHealth(ctx, port, path, label, timeout, nil)
}

func waitForHTTPHealth(ctx context.Context, port int, path string, label string, timeout time.Duration, waitErr <-chan error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	for {
		if httpOK(ctx, url, 3*time.Second) {
			return nil
		}
		select {
		case err := <-waitErr:
			if err != nil {
				return fmt.Errorf("%s exited before becoming healthy: %w", label, err)
			}
			return fmt.Errorf("%s exited before becoming healthy", label)
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s at %s", label, url)
		case <-ticker.C:
		}
	}
}

func httpOK(ctx context.Context, url string, timeout time.Duration) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client := http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func waitForTCP(ctx context.Context, host string, port int, label string, timeout time.Duration) error {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	dialer := net.Dialer{Timeout: time.Second}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s at %s", label, address)
		case <-ticker.C:
		}
	}
}

func tcpOK(ctx context.Context, host string, port int, timeout time.Duration) bool {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
