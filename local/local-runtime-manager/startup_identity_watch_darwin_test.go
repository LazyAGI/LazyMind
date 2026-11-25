package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinStartupGenerationEvidence(t *testing.T) {
	_, file, _ := startupIdentityTestInstallation(t)
	before, err := startupObjectGeneration(file)
	if err != nil || before == 0 {
		t.Fatalf("initial generation: %d, %v", before, err)
	}
	for _, content := range []string{"other", "first"} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := startupObjectGeneration(file)
	if err != nil || after == before {
		t.Fatalf("restored data lost its write history: %d -> %d, %v", before, after, err)
	}

	f, err := os.OpenFile(file, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := unix.Mmap(int(f.Fd()), 0, 5, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'x'
	_, err = startupObjectGeneration(file)
	if unmapErr := unix.Munmap(data); unmapErr != nil {
		t.Fatal(unmapErr)
	}
	if !errors.Is(err, errStartupObservationUnavailable) {
		t.Fatalf("accepted an unobservable writable mapping: %v", err)
	}

	_, detached, _ := startupIdentityTestInstallation(t)
	if err := unix.Setxattr(detached, "com.apple.cs.CodeDirectory", []byte("test"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := startupObjectGeneration(detached); !errors.Is(err, errStartupObservationUnavailable) {
		t.Fatalf("accepted signature metadata outside the data fork: %v", err)
	}
}

func TestDarwinStartupWatchPinsBundleAncestors(t *testing.T) {
	for _, target := range []string{"bundle", "ancestor"} {
		t.Run(target, func(t *testing.T) {
			paths, _, _ := startupIdentityTestInstallation(t)
			bundle := filepath.Dir(filepath.Dir(filepath.Dir(paths.ResourcesRoot)))
			parent := filepath.Dir(bundle)
			unchanged, release, err := watchStartupIdentityInputs(paths)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if err := os.WriteFile(filepath.Join(parent, "unrelated"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if !unchanged() {
				t.Fatal("unrelated sibling write invalidated the bundle")
			}
			path := bundle
			if target == "ancestor" {
				path = parent
			}
			if err := os.Rename(path, path+".moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".moved", path); err != nil {
				t.Fatal(err)
			}
			results := make(chan bool, 2)
			for i := 0; i < cap(results); i++ {
				go func() { results <- unchanged() }()
			}
			for i := 0; i < cap(results); i++ {
				if <-results {
					t.Fatal("a waiter accepted the restored path")
				}
			}
			release()
			if unchanged() {
				t.Fatal("a closed observation remained valid")
			}
		})
	}
}
