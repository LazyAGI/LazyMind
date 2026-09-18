package providerconnection

import (
	_ "embed"
	"encoding/json"
)

// This manifest is also read by the macOS, Windows and Docker builds.
//
//go:embed feishu-cli-release.json
var feishuCLIReleaseJSON []byte

var FeishuCLIVersion = func() string {
	var release struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(feishuCLIReleaseJSON, &release); err != nil || release.Version == "" {
		panic("invalid embedded Feishu CLI release manifest")
	}
	return release.Version
}()
