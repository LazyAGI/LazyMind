package localworkspace

import "testing"

func TestWindowsPersistentIdentityPreservesFullFileAndVolumeIDs(t *testing.T) {
	first := [16]byte{1}
	second := first
	second[15] = 1
	if windowsPersistentIdentity(1, first) == windowsPersistentIdentity(1, second) {
		t.Fatal("lost high file ID bits")
	}
	if windowsPersistentIdentity(1, first) == windowsPersistentIdentity(1<<32|1, first) {
		t.Fatal("lost high volume serial bits")
	}
}
