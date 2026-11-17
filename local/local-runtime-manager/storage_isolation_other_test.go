//go:build !windows

package main

import "testing"

func runStorageIsolated(m *testing.M) int { return m.Run() }
