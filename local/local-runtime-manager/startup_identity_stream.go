package main

import (
	"context"
	"errors"
	"hash"
	"io"
	"os"
)

var errStartupObservationUnavailable = errors.New("startup signature inputs cannot be observed")

func hashStartupIdentityBytes(ctx context.Context, digest hash.Hash, file *os.File, buf []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Read(buf)
		digest.Write(buf[:n])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
