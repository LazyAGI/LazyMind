package desktopsetup

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func Running(ctx context.Context, application string) (bool, error) {
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", `ObjC.import('AppKit'); function run(args) {
 const apps = $.NSWorkspace.sharedWorkspace.runningApplications;
 for (let i = 0; i < apps.count; i++) {
  const url = apps.objectAtIndex(i).bundleURL;
  if (url && ObjC.unwrap(url.path) === args[0]) return 'true';
 }
 return 'false';
}`, application).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect running application: %s: %w", output, err)
	}
	return strings.TrimSpace(string(output)) == "true", nil
}

func ListenerMatches(ctx context.Context, application string, port int) bool {
	output, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return false
	}
	pids := strings.Fields(string(output))
	if len(pids) == 0 {
		return false
	}
	// Helpers outside the app bundle can inherit the listening descriptor.
	// Take one process-tree snapshot so those helpers can be traced back to a
	// listener in the expected application, without accepting unrelated owners.
	processes, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,comm=").Output()
	return err == nil && listenerProcessesMatch(application, pids, string(processes))
}

func listenerProcessesMatch(application string, listeners []string, snapshot string) bool {
	type process struct{ parent, executable string }
	processes := map[string]process{}
	for _, line := range strings.Split(snapshot, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		processes[fields[0]] = process{fields[1], strings.Join(fields[2:], " ")}
	}
	owners := map[string]bool{}
	for _, pid := range listeners {
		if strings.HasPrefix(processes[pid].executable, application+"/Contents/") {
			owners[pid] = true
		}
	}
	if len(owners) == 0 {
		return false
	}
	for _, pid := range listeners {
		seen := map[string]bool{}
		for !owners[pid] {
			p, exists := processes[pid]
			if !exists || seen[pid] {
				return false
			}
			seen[pid] = true
			pid = p.parent
		}
	}
	return true
}
