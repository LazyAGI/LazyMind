//go:build !linux && !darwin && !windows

package main

func processStartIdentity(int) uint64 { return 0 }
