package codexruntime

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateOwnerPolicy(t *testing.T) {
	user, err := windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, owner       string
		elevated, allowed bool
	}{
		{"user", user.String(), false, true},
		{"elevated-user", user.String(), true, true},
		{"elevated-administrators", "S-1-5-32-544", true, true},
		{"unelevated-administrators", "S-1-5-32-544", false, false},
		{"another-user", "S-1-5-21-1-2-3-1002", true, false},
		{"system", "S-1-5-18", true, false},
		{"missing-owner", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var owner *windows.SID
			if tc.owner != "" {
				owner, err = windows.StringToSid(tc.owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got := canPrivatizeOwner(owner, user, tc.elevated); got != tc.allowed {
				t.Fatalf("allowed=%v, want %v", got, tc.allowed)
			}
		})
	}
}

func TestMakePrivateNormalizesElevatedOwner(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an elevated token, as used by Windows CI")
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"directory", "file"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			if kind == "file" {
				path = filepath.Join(path, "state.json")
				if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, admin, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := checkPrivate(path); err == nil {
				t.Fatal("reader accepted group ownership before normalization")
			}
			if err := makePrivate(path); err != nil {
				t.Fatal(err)
			}
			if err := checkPrivate(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}
