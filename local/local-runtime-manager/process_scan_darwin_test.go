//go:build darwin

package main

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestParseDarwinProcessArgsSeparatesArgumentsAndOwnership(t *testing.T) {
	args := []string{"/repo/bin/python", "-c", "", runtimeProcessOwnerEnvVar + "=/spoofed"}
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, uint32(len(args)))
	raw = append(raw, []byte("/repo/bin/python\x00\x00\x00")...)
	for _, arg := range args {
		raw = append(raw, append([]byte(arg), 0)...)
	}
	raw = append(raw, []byte("OTHER=value\x00"+runtimeProcessOwnerEnvVar+"=/owned\x00")...)
	got, owner := parseDarwinProcessArgs(raw)
	if !reflect.DeepEqual(got, args) || owner != "/owned" {
		t.Fatalf("args=%q owner=%q", got, owner)
	}
	for _, invalid := range [][]byte{nil, {1, 0, 0, 0}, raw[:len(raw)/2]} {
		if _, owner := parseDarwinProcessArgs(invalid); owner != "" {
			t.Fatalf("malformed args supplied owner %q", owner)
		}
	}
}
