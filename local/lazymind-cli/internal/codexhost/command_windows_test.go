package codexhost

import (
	"context"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBackgroundCommandHidesConsole(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := backgroundCommand(ctx, `C:\Program Files\nodejs\node.exe`, "probe.mjs", "--probe-only")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("background command can open a console")
	}
	if !reflect.DeepEqual(cmd.Args, []string{`C:\Program Files\nodejs\node.exe`, "probe.mjs", "--probe-only"}) || cmd.Cancel == nil {
		t.Fatal("arguments or context cancellation lost")
	}
}

func TestCodexHostDiscoveryIncludesMSIX(t *testing.T) {
	spec := desktopApplicationSpec()
	if !reflect.DeepEqual(spec.PackageNames, []string{"OpenAI.Codex"}) {
		t.Fatal("installation and recovery must discover the MSIX desktop")
	}
}
