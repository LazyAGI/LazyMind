//go:build windows

package main

import (
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
)

func runStorageIsolated(m *testing.M) int { return teststorage.Run(m) }
