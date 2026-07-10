package testutil

import (
	"context"
	"errors"
)

type UploadSession struct {
	UploadID    string
	OwnerUserID string
	State       string
	StoredPath  string
	Filename    string
}

type FakeUploadStore struct {
	Sessions map[string]UploadSession
}

func NewFakeUploadStore() *FakeUploadStore {
	return &FakeUploadStore{Sessions: map[string]UploadSession{}}
}

func (s *FakeUploadStore) Put(session UploadSession) {
	s.Sessions[session.UploadID] = session
}

func (s *FakeUploadStore) Get(ctx context.Context, uploadID string) (UploadSession, error) {
	session, ok := s.Sessions[uploadID]
	if !ok {
		return UploadSession{}, errors.New("upload session not found")
	}
	return session, nil
}
