package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestOwnedRuntimeProcessesRequiresOwnership(t *testing.T) {
	paths := RuntimePaths{RepoRoot: "/repo", BuildRoot: "/repo/local/build", RuntimeRoot: "/runtime", BinDir: "/repo/local/build/bin", CoreBin: "/repo/local/build/bin/core"}
	registered := []LocalProcessRecord{
		{PID: 800001, Service: "core", RuntimeRoot: paths.RuntimeRoot, StartID: 100},
		{PID: 800007, Service: "core", RuntimeRoot: paths.RuntimeRoot, StartID: 99},
		{PID: 800008, Service: "core", RepoRoot: paths.RepoRoot, RuntimeRoot: "/another-runtime", StartID: 100},
		// Neither a current pidfile observation nor the owner label can rescue a reused PID.
		{PID: 800007, Service: "core", RuntimeRoot: paths.RuntimeRoot},
		// A stale entry pointing at a Connector still cannot authorize its cleanup.
		{PID: 800004, Service: "core", RuntimeRoot: paths.RuntimeRoot, StartID: 100},
	}
	inventory := []LocalProcessRecord{
		{PID: 800003, ParentPID: 800002, Executable: "/usr/bin/python", StartID: 102},
		{PID: 800002, ParentPID: 800001, Executable: "/usr/bin/python", StartID: 101},
		{PID: 800001, Executable: paths.CoreBin, StartID: 100},
		{PID: 800004, Executable: paths.BinDir + "/lazymind", Command: []string{"lazymind", "mcp", "proxy"}, OwnerRoot: paths.RuntimeRoot, StartID: 100},
		{PID: 800005, Executable: paths.BinDir + "/unrelated", Command: []string{"unrelated", paths.RuntimeRoot}, StartID: 100},
		{PID: 800006, Executable: "/usr/bin/python", OwnerRoot: paths.RuntimeRoot, StartID: 100},
		{PID: 800007, Executable: paths.CoreBin, OwnerRoot: paths.RuntimeRoot, StartID: 100},
		{PID: 800008, Executable: paths.CoreBin, StartID: 100},
		{PID: 800009, Executable: paths.CoreBin, OwnerRoot: "/another-runtime", StartID: 100},
		{PID: 800010, Executable: "/usr/bin/python", ParentPID: 800001, StartID: 90},
		{PID: 800011, Executable: "/usr/bin/python", OwnerRoot: paths.RuntimeRoot},
		{PID: 800012, Executable: "/usr/bin/node", ParentPID: 800004, OwnerRoot: paths.RuntimeRoot, StartID: 103},
		// A shared group, directory, or similar service path does not prove ownership.
		{PID: 800013, Executable: paths.CoreBin + "-other", PGID: 800001, StartID: 100},
		{PID: 800014, Executable: "/repo/local/build-other/bin/core", StartID: 100},
	}
	var skipped []int
	got := ownedRuntimeProcesses(paths, registered, inventory, func(record LocalProcessRecord) { skipped = append(skipped, record.PID) })
	var pids []int
	for _, record := range got {
		pids = append(pids, record.PID)
		if record.RuntimeRoot != paths.RuntimeRoot || record.StartID == 0 {
			t.Fatalf("unverified record: %+v", record)
		}
	}
	sort.Ints(pids)
	if want := []int{800001, 800002, 800003, 800006}; !reflect.DeepEqual(pids, want) {
		t.Fatalf("cleanup PIDs = %v, want %v", pids, want)
	}
	if len(skipped) == 0 {
		t.Fatal("ambiguous directory matches produced no diagnostic")
	}
}

func TestConnectorExecutableRecognition(t *testing.T) {
	for _, name := range []string{"/build/bin/lazymind", "/resources/bin/lazymind.exe", `C:\LazyMind\bin\LAZYMIND.EXE`} {
		if !isConnectorExecutable(name) {
			t.Errorf("Connector was not protected: %s", name)
		}
	}
	for _, name := range []string{"/build/bin/local-runtime-manager", "/build/bin/lazymind-helper", "/lazymind/bin/python"} {
		if isConnectorExecutable(name) {
			t.Errorf("unrelated executable classified as Connector: %s", name)
		}
	}
}

func TestLegacyRuntimePidfileRequiresExactService(t *testing.T) {
	paths := RuntimePaths{RepoRoot: "/repo", RuntimeRoot: "/runtime", CoreBin: "/repo/bin/core"}
	for _, executable := range []string{paths.CoreBin, paths.CoreBin + "-other", "/repo/bin/lazymind"} {
		records := ownedRuntimeProcesses(paths,
			[]LocalProcessRecord{{PID: 800001, RuntimeRoot: paths.RuntimeRoot}},
			[]LocalProcessRecord{{PID: 800001, Executable: executable, StartID: 10}}, nil)
		want := 0
		if executable == paths.CoreBin {
			want = 1
		}
		if len(records) != want {
			t.Errorf("legacy pidfile for %q authorized %d processes, want %d", executable, len(records), want)
		}
	}
}

func TestRememberRuntimeDescendantsSurviveReparenting(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, RuntimeRoot: filepath.Join(root, "runtime"), ProcessRegistryFile: filepath.Join(root, "run", "processes.json")}
	registered := []LocalProcessRecord{{PID: 800001, RuntimeRoot: paths.RuntimeRoot, StartID: 10, Service: "core"}}
	inventory := []LocalProcessRecord{
		{PID: 800001, Executable: "/core", StartID: 10},
		{PID: 800002, ParentPID: 800001, Executable: "/python", StartID: 11},
		{PID: 800003, ParentPID: 800001, Executable: "/python", StartID: 12},
	}
	if err := rememberLocalProcessRecords(paths, ownedRuntimeProcesses(paths, registered, inventory, nil)); err != nil {
		t.Fatal(err)
	}
	registry, err := readLocalProcessRegistry(paths)
	if err != nil || len(registry.Processes) != 3 {
		t.Fatalf("saved inventory = %+v, error = %v", registry, err)
	}
	inventory = inventory[1:]
	for i := range inventory {
		inventory[i].ParentPID = 1
	}
	got := ownedRuntimeProcesses(paths, registry.Processes, inventory, nil)
	if len(got) != 2 {
		t.Fatalf("detached workers were lost: %+v", got)
	}
}

func TestProcessIdentityIsAvailable(t *testing.T) {
	if id := processStartIdentity(os.Getpid()); id == 0 {
		t.Fatal("current process has no start identity")
	}
}

func TestRuntimeWorkersCarryOwnershipLabel(t *testing.T) {
	paths := RuntimePaths{RuntimeRoot: filepath.Join(t.TempDir(), "runtime"), RepoRoot: t.TempDir()}
	assertEnvContains(t, serviceRuntimeEnv(paths), runtimeProcessOwnerEnvVar+"="+paths.RuntimeRoot)
	assertEnvContains(t, processComposeRuntimeEnv(paths), runtimeProcessOwnerEnvVar+"="+paths.RuntimeRoot)
}

func TestDesktopOwnerDoesNotHideRegisteredRuntimeChildren(t *testing.T) {
	t.Setenv(desktopOwnerPIDEnvVar, "800100")
	paths := RuntimePaths{RuntimeRoot: "/runtime", RepoRoot: "/repo"}
	inventory := []LocalProcessRecord{
		{PID: 800100, Executable: `C:\LazyMind\LazyMind.exe`, StartID: 1},
		{PID: 800101, ParentPID: 800100, Executable: `C:\LazyMind\renderer.exe`, StartID: 2},
		{PID: 800102, ParentPID: 800100, Executable: `C:\LazyMind\core.exe`, StartID: 3},
		{PID: 800103, ParentPID: 800102, Executable: `C:\Python\python.exe`, StartID: 4},
		{PID: 800104, ParentPID: 800100, Executable: `C:\LazyMind\bin\lazymind.exe`, StartID: 5},
		{PID: 800105, ParentPID: 800104, Executable: `C:\Program Files\node.exe`, StartID: 6},
	}
	registered := []LocalProcessRecord{
		{PID: 800100, RuntimeRoot: paths.RuntimeRoot, StartID: 1},
		{PID: 800102, RuntimeRoot: paths.RuntimeRoot, StartID: 3},
		{PID: 800104, RuntimeRoot: paths.RuntimeRoot, StartID: 5},
	}
	got := ownedRuntimeProcesses(paths, registered, inventory, nil)
	var pids []int
	for _, record := range got {
		pids = append(pids, record.PID)
	}
	sort.Ints(pids)
	if !reflect.DeepEqual(pids, []int{800102, 800103}) {
		t.Fatalf("desktop cleanup = %v", pids)
	}
}

func TestCleanupRegistryKeepsReplacementProcess(t *testing.T) {
	paths := RuntimePaths{RepoRoot: t.TempDir(), ProcessRegistryFile: filepath.Join(t.TempDir(), "processes.json")}
	old := LocalProcessRecord{PID: 800001, Service: "core", StartID: 10}
	replacement := old
	replacement.StartID = 11
	if err := rememberLocalProcessRecords(paths, []LocalProcessRecord{replacement}); err != nil {
		t.Fatal(err)
	}
	cleanupLocalProcessRecords(paths, []LocalProcessRecord{old})
	registry, err := readLocalProcessRegistry(paths)
	if err != nil || len(registry.Processes) != 1 || registry.Processes[0].StartID != replacement.StartID {
		t.Fatalf("replacement registration was removed: %+v / %v", registry, err)
	}
}
