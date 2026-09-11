package browser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPairingIsOneTimeAndDevicesAreUserScoped(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }

	pairing, err := hub.CreatePairing("user-1")
	if err != nil {
		t.Fatal(err)
	}
	paired, err := hub.PairExtension(PairExtensionInput{
		Code: pairing.Code, DeviceName: "Work Chrome", Browser: "Chrome", Version: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if paired.DeviceID == "" || paired.DeviceToken == "" {
		t.Fatalf("invalid pair result: %#v", paired)
	}
	if _, err := hub.PairExtension(PairExtensionInput{Code: pairing.Code}); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("reused pairing error = %v, want ErrPairingInvalid", err)
	}
	if _, err := hub.AuthenticateDevice(paired.DeviceID, "wrong"); err == nil {
		t.Fatal("wrong device token authenticated")
	}
	if _, err := hub.AuthenticateDevice(paired.DeviceID, paired.DeviceToken); err != nil {
		t.Fatalf("valid device token rejected: %v", err)
	}
	if got := hub.ListDevices("user-1"); len(got) != 1 || got[0].Name != "Work Chrome" {
		t.Fatalf("user devices = %#v", got)
	}
	if got := hub.ListDevices("user-2"); len(got) != 0 {
		t.Fatalf("other user can see devices: %#v", got)
	}
	if err := hub.RevokeDevice("user-2", paired.DeviceID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("cross-user revoke error = %v", err)
	}
}

func TestEdgePairingPreservesBrowserAndExtensionVersions(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := hub.CreatePairing("user-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.PairExtension(PairExtensionInput{
		Code: pairing.Code, DeviceName: "Windows Microsoft Edge", Browser: "Microsoft Edge",
		BrowserVersion: "140.0.3485.54", ExtensionVersion: "0.2.0", Version: "0.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	devices := hub.ListDevices("user-1")
	if len(devices) != 1 {
		t.Fatalf("devices = %#v", devices)
	}
	device := devices[0]
	if device.Browser != "Microsoft Edge" || device.BrowserVersion != "140.0.3485.54" ||
		device.ExtensionVersion != "0.2.0" || device.Version != "0.2.0" {
		t.Fatalf("Edge metadata = %#v", device)
	}
}

func TestRevokeAllDevicesIsUserScopedAndClosesConnections(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	firstPairing, _ := hub.CreatePairing("user-1")
	first, _ := hub.PairExtension(PairExtensionInput{Code: firstPairing.Code})
	secondPairing, _ := hub.CreatePairing("user-1")
	second, _ := hub.PairExtension(PairExtensionInput{Code: secondPairing.Code})
	otherPairing, _ := hub.CreatePairing("user-2")
	_, _ = hub.PairExtension(PairExtensionInput{Code: otherPairing.Code})
	connection := &deviceConnection{done: make(chan struct{}), pending: make(map[string]chan commandResponse)}
	if err := hub.Attach(first.DeviceID, connection); err != nil {
		t.Fatal(err)
	}

	if revoked := hub.RevokeAllDevices("user-1"); revoked != 2 {
		t.Fatalf("revoked = %d, want 2", revoked)
	}
	select {
	case <-connection.done:
	default:
		t.Fatal("online device connection was not closed")
	}
	if len(hub.ListDevices("user-1")) != 0 {
		t.Fatal("revoked user still has devices")
	}
	if len(hub.ListDevices("user-2")) != 1 {
		t.Fatal("another user's device was revoked")
	}
	if _, err := hub.AuthenticateDevice(second.DeviceID, second.DeviceToken); err == nil {
		t.Fatal("revoked device credentials still authenticate")
	}
}

func TestExpiredPairingIsRejected(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }
	pairing, err := hub.CreatePairing("user-1")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(defaultPairingTTL + time.Second)
	if _, err := hub.PairExtension(PairExtensionInput{Code: pairing.Code}); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("expired pairing error = %v", err)
	}
}

func TestCallRoutesCommandAndResultToOwnedOnlineDevice(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	pairing, _ := hub.CreatePairing("user-1")
	paired, _ := hub.PairExtension(PairExtensionInput{Code: pairing.Code})
	connection := &deviceConnection{
		send: make(chan commandEnvelope, 1), done: make(chan struct{}),
		pending: make(map[string]chan commandResponse),
	}
	if err := hub.Attach(paired.DeviceID, connection); err != nil {
		t.Fatal(err)
	}
	go func() {
		command := <-connection.send
		if command.Action != "snapshot" {
			connection.resolve(resultEnvelope{Type: "result", ID: command.ID, OK: false, Error: &protocolError{Code: "BAD_ACTION"}})
			return
		}
		connection.resolve(resultEnvelope{Type: "result", ID: command.ID, OK: true, Result: json.RawMessage(`{"revision":2}`)})
	}()

	raw, err := hub.Call(context.Background(), "user-1", paired.DeviceID, "snapshot", SessionInput{SessionID: "bs_1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"revision":2}` {
		t.Fatalf("result = %s", raw)
	}
	if _, err := hub.Call(context.Background(), "user-2", paired.DeviceID, "snapshot", SessionInput{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("cross-user call error = %v", err)
	}
	if _, err := hub.Call(context.Background(), "user-1", paired.DeviceID, "raw_cdp", map[string]any{}); err == nil {
		t.Fatal("unsupported action was accepted")
	}
}

func TestSummarizeBrowserCommandResultUsesExtensionMetrics(t *testing.T) {
	raw := json.RawMessage(`{
		"revision": 7,
		"elements": [{"role":"button"},{"role":"StaticText"}],
		"limitations": ["snapshot_element_limit_reached"],
		"snapshot_metrics": {
			"raw_ax_nodes": 900,
			"included_elements": 2,
			"invisible_text_nodes": 304,
			"role_counts": {"button":1,"StaticText":1},
			"snapshot_ms": 42
		}
	}`)
	summary := summarizeBrowserCommandResult(raw)
	if summary.ResponseBytes != len(raw) || summary.Revision != 7 || summary.ElementCount != 2 ||
		summary.RawAXNodeCount != 900 || summary.InvisibleTextNodes != 304 || summary.SnapshotMS != 42 {
		t.Fatalf("summary = %#v", summary)
	}
	if got := formatBrowserRoleCounts(summary.RoleCounts); got != "StaticText:1,button:1" {
		t.Fatalf("role counts = %q", got)
	}
}

func TestSummarizeBrowserCommandResultFallsBackToElements(t *testing.T) {
	raw := json.RawMessage(`{"revision":2,"elements":[{"role":"button"},{"role":"button"},{"role":"textbox"}]}`)
	summary := summarizeBrowserCommandResult(raw)
	if summary.ElementCount != 3 || summary.RoleCounts["button"] != 2 || summary.RoleCounts["textbox"] != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestOnlineDevicePrefersConfiguredBrowser(t *testing.T) {
	t.Setenv("LAZYMIND_BROWSER_PREFERRED_DEVICE_BROWSER", "Edge")
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	chromeConnection := &deviceConnection{done: make(chan struct{})}
	edgeConnection := &deviceConnection{done: make(chan struct{})}
	now := time.Now().UTC()
	hub.devices["chrome"] = &deviceRecord{
		ID: "chrome", UserID: "user-1", Browser: "Chrome",
		LastSeenAt: now, Connection: chromeConnection,
	}
	hub.devices["edge"] = &deviceRecord{
		ID: "edge", UserID: "user-1", Browser: "Edge",
		LastSeenAt: now.Add(-time.Hour), Connection: edgeConnection,
	}

	selected, err := hub.onlineDevice("user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if selected != edgeConnection {
		t.Fatalf("selected %#v, want configured Edge browser", selected)
	}
}

func TestAllowedActionIncludesEveryPublishedBrowserAction(t *testing.T) {
	for _, name := range ToolNames {
		action := strings.TrimPrefix(name, "browser.")
		if !allowedAction(action) {
			t.Fatalf("published tool action %q is blocked by the hub allowlist", action)
		}
	}
	if allowedAction("raw_cdp") {
		t.Fatal("raw CDP action must remain blocked")
	}
	if allowedAction("click_at") {
		t.Fatal("coordinate click must not be exposed")
	}
	for _, name := range ToolNames {
		if name == "browser.click_at" {
			t.Fatal("coordinate click must not be published")
		}
	}
}

func TestToolTokenRoundTripAndExpiry(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 10, 15, 0, 0, time.UTC)
	hub.signer.now = func() time.Time { return now }
	token, err := hub.ToolToken("user-1")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := hub.VerifyToolToken(token)
	if err != nil || claims.Subject != "user-1" {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	now = time.Unix(claims.Expires, 0).Add(time.Second)
	if _, err := hub.VerifyToolToken(token); err == nil {
		t.Fatal("expired token verified")
	}
}
