package localworkspace

import (
	"syscall"
	"testing"
)

func TestDarwinPersistentIdentitySurvivesDeviceRenumbering(t *testing.T) {
	volume := [16]byte{1, 2, 3}
	before := syscall.Stat_t{Dev: 16777232, Ino: 130602869}
	after := before
	after.Dev = 16777234
	if darwinPersistentIdentity(volume, &before) != darwinPersistentIdentity(volume, &after) {
		t.Fatal("mount device number changed persistent identity")
	}
	after.Ino++
	if darwinPersistentIdentity(volume, &before) == darwinPersistentIdentity(volume, &after) ||
		darwinPersistentIdentity(volume, &before) == darwinPersistentIdentity([16]byte{2}, &before) {
		t.Fatal("different object or volume reused persistent identity")
	}
}
