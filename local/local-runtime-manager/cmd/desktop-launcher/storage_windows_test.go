//go:build windows

package main

import (
	"os"
	"reflect"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
)

func TestMain(m *testing.M) { os.Exit(teststorage.Run(m)) }

func TestWindowsLauncherClearsInheritedStorageRootWithoutRedirectingSystemPaths(t *testing.T) {
	input := []string{
		"LAZYMIND_DESKTOP_DATA_ROOT=D:\\untrusted\\Data",
		"lazymind_desktop_data_root=E:\\stale\\Data",
		"LOCALAPPDATA=C:\\Users\\fixture\\AppData\\Local",
		"USERPROFILE=C:\\Users\\fixture",
		"APPDATA=C:\\Users\\fixture\\AppData\\Roaming",
		"PATH=C:\\Windows\\System32",
	}
	want := input[2:]
	got, _ := sanitizedEnvironment(input)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launcher inherited storage/system environment = %#v, want %#v", got, want)
	}
}
