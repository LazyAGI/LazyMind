package mcpclient

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

const dshWorkflowRow = "lazymind-workflow-panel"
const dshWorkflowPackage = "@lazymind/dsh-workflow"

//go:embed assets/dsh-workflow.tgz
var dshWorkflowArchive []byte

func dshBundleHash() string {
	sum := sha256.Sum256(dshWorkflowArchive)
	return hex.EncodeToString(sum[:])
}

func dshProfileName() string {
	if name := strings.TrimSpace(os.Getenv("LAZYMIND_DSH_PROFILE")); name != "" {
		return name
	}
	return "web"
}

// Resolve both ordinary profile-local and existing hoisted pnpm installations.
func dshModuleVersion(profile, module string) (string, error) {
	for _, parent := range []string{profile, filepath.Dir(profile), filepath.Join(profile, "node_modules", ".pnpm")} {
		body, err := os.ReadFile(filepath.Join(parent, "node_modules", filepath.FromSlash(module), "package.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			return "", err
		}
		return manifest.Version, nil
	}
	return "", os.ErrNotExist
}

func runDSHPlugin(ctx context.Context, profile string, args ...string) error {
	dir := filepath.Join(dshHome(), "profiles", profile)
	if !pathExists(filepath.Join(dir, "package.json")) {
		return errors.New("DeepSeek Harness is not initialized. Start DSH once, then enable this switch to install the LazyMind plugin")
	}
	pnpm, err := exec.LookPath("pnpm")
	if err != nil {
		return errors.New("pnpm is required to install the LazyMind plugin into the existing DSH profile")
	}
	command := exec.CommandContext(ctx, pnpm, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "DSH_HOME="+dshHome())
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 2048 {
			message = message[len(message)-2048:]
		}
		return fmt.Errorf("install DSH workflow bundle: %w: %s", err, message)
	}
	return reconcileProfileBundles(dir)
}

func hasDSHBundlePatch(profileDir, packageName string) bool {
	for _, parent := range []string{profileDir, filepath.Dir(profileDir), filepath.Join(profileDir, "node_modules", ".pnpm")} {
		body, err := os.ReadFile(filepath.Join(parent, "node_modules", filepath.FromSlash(packageName), "package.json"))
		if err != nil {
			continue
		}
		var manifest struct {
			DSH struct {
				Bundle struct {
					Patch string `json:"patch"`
				} `json:"bundle"`
			} `json:"dsh"`
		}
		if json.Unmarshal(body, &manifest) == nil && manifest.DSH.Bundle.Patch != "" {
			return true
		}
	}
	return false
}

func reconcileProfileBundles(profileDir string) error {
	path := filepath.Join(profileDir, "package.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return err
	}
	deps, _ := manifest["dependencies"].(map[string]any)
	dsh, _ := manifest["dsh"].(map[string]any)
	if dsh == nil {
		dsh = map[string]any{}
		manifest["dsh"] = dsh
	}
	profile, _ := dsh["profile"].(map[string]any)
	if profile == nil {
		profile = map[string]any{}
		dsh["profile"] = profile
	}
	bundles := jsonStrings(profile["bundles"])
	changed := false
	for name := range deps {
		if !hasDSHBundlePatch(profileDir, name) || containsString(bundles, name) {
			continue
		}
		bundles = append(bundles, name)
		changed = true
	}
	if !changed {
		return nil
	}
	profile["bundles"] = bundles
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func jsonStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func configureDSHWorkflow(path, pairingFile, webURL, bridgeURL string, disabled bool) error {
	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	sequence := yamlSequence(document)
	var existing *yaml.Node
	for _, item := range sequence.Content {
		if scalarValue(item, "id") == dshWorkflowRow {
			existing = item
			break
		}
	}
	value := map[string]any{"id": dshWorkflowRow, "disabled": disabled}
	if !disabled {
		value["config"] = map[string]string{"serverName": "lazymind", "pairingFile": pairingFile, "webUrl": webURL, "bridgeUrl": bridgeURL, "bundleHash": dshBundleHash()}
	}
	var replacement yaml.Node
	if err := replacement.Encode(value); err != nil {
		return err
	}
	if existing != nil {
		*existing = replacement
	} else {
		sequence.Content = append(sequence.Content, &replacement)
	}
	body, err := encodeYAML(document)
	if err != nil {
		return err
	}
	return writeConfigFile(path, body)
}

func (a *Adapter) dshWorkflowConfigured() bool {
	document, err := readYAMLDocument(configPath(DeepSeekHarness))
	if err != nil {
		return false
	}
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return false
	}
	account, err := store.AccountScope()
	if err != nil {
		return false
	}
	if _, err := dshModuleVersion(filepath.Dir(configPath(DeepSeekHarness)), dshWorkflowPackage); err != nil {
		return false
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow || scalarValue(item, "disabled") == "true" {
			continue
		}
		config := mappingValue(item, "config")
		if scalarValue(config, "bundleHash") != dshBundleHash() {
			return false
		}
		file := scalarValue(config, "pairingFile")
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if filepath.Clean(file) != filepath.Join(a.home, "workflow-hosts", id+".json") {
			return false
		}
		return workflowhost.Configured(a.home, id, account)
	}
	return false
}

func (a *Adapter) installDSHWorkflow(ctx context.Context) (bool, error) {
	profile := filepath.Dir(configPath(DeepSeekHarness))
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return false, err
	}
	api, err := coreapi.New(store)
	if err != nil {
		return false, err
	}
	var capability struct {
		SchemaReady bool   `json:"schema_ready"`
		Protocol    string `json:"protocol"`
	}
	if err := api.DoJSON(ctx, "GET", "/workflow-control/capabilities", nil, &capability); err != nil {
		return false, err
	}
	if !capability.SchemaReady || capability.Protocol != "workflow.control.v1" {
		return false, errors.New("update LazyMind Core and its database before enabling workflow control")
	}
	account, err := store.AccountScope()
	if err != nil {
		return false, err
	}
	pair, err := workflowhost.Ensure(a.home, profile, account)
	if err != nil {
		return false, err
	}
	webURL := strings.TrimSpace(os.Getenv("LAZYMIND_WEB_URL"))
	if webURL == "" {
		webURL, err = store.ServerURL(ctx)
		if err != nil {
			return false, err
		}
	}
	bridgeURL := strings.TrimSpace(os.Getenv("LAZYMIND_ASSISTANT_BRIDGE_URL"))
	if bridgeURL == "" {
		bridgeURL = "http://127.0.0.1:19091"
	}
	archive := filepath.Join(a.home, "bundles", "dsh-workflow-"+dshBundleHash()[:16]+".tgz")
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		return false, err
	}
	if err := os.WriteFile(archive, dshWorkflowArchive, 0600); err != nil {
		return false, err
	}
	run := a.dshRunner
	if run == nil {
		run = runDSHPlugin
	}
	if err := run(ctx, dshProfileName(), "add", "--workspace-root", archive); err != nil {
		return false, err
	}
	pairingFile := filepath.Join(a.home, "workflow-hosts", pair.ConnectorID+".json")
	if err := configureDSHWorkflow(configPath(DeepSeekHarness), pairingFile, webURL, bridgeURL, false); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Adapter) disconnectDSHWorkflow() error {
	document, err := readYAMLDocument(configPath(DeepSeekHarness))
	if err != nil {
		return err
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow {
			continue
		}
		file := scalarValue(mappingValue(item, "config"), "pairingFile")
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if file != "" {
			if err := workflowhost.Disable(a.home, id); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return configureDSHWorkflow(configPath(DeepSeekHarness), "", "", "", true)
	}
	return nil
}
