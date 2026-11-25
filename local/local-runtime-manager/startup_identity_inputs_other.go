//go:build !darwin && !windows && !linux

package main

import (
	"context"
	"errors"
)

func snapshotStartupIdentityInputs(context.Context, RuntimePaths) ([32]byte, error) {
	return [32]byte{}, errors.ErrUnsupported
}

func watchStartupIdentityInputs(RuntimePaths) (func() bool, func(), error) {
	return nil, nil, errors.ErrUnsupported
}
