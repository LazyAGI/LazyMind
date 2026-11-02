package desktopsetup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func inspectProcess(ctx context.Context, application, script string, port int) (bool, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop';"+script)
	cmd.Env = append(os.Environ(), "LAZYMIND_INSPECT_APP="+application, "LAZYMIND_INSPECT_PORT="+strconv.Itoa(port))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect running application: %s: %w", output, err)
	}
	return strings.EqualFold(strings.TrimSpace(string(output)), "true"), nil
}
func Running(ctx context.Context, application string) (bool, error) {
	return inspectProcess(ctx, application, `@((Get-CimInstance Win32_Process) | Where-Object { $_.ExecutablePath -eq $env:LAZYMIND_INSPECT_APP }).Count -gt 0`, 0)
}
func ListenerMatches(ctx context.Context, application string, port int) bool {
	ok, err := inspectProcess(ctx, application, `
$listeners = @(Get-NetTCPConnection -State Listen -LocalPort ([int]$env:LAZYMIND_INSPECT_PORT) -ErrorAction SilentlyContinue)
if ($listeners.Count -eq 0) { return $false }
foreach ($listener in $listeners) {
 $p = Get-CimInstance Win32_Process -Filter ('ProcessId = ' + $listener.OwningProcess)
 if ($p.ExecutablePath -ne $env:LAZYMIND_INSPECT_APP) { return $false }
}
$true`, port)
	return err == nil && ok
}
