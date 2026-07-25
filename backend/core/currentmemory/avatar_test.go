package currentmemory

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestAvatarHandlersStoreReadReplaceDeleteAndIsolateUsers(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)
	repository := NewRepository(db.DB)
	if err := repository.EnsureInitialized(t.Context(), "user-1"); err != nil {
		t.Fatalf("initialize memory: %v", err)
	}
	soulBefore, err := repository.GetEntry(t.Context(), "user-1", SoulPath)
	if err != nil {
		t.Fatalf("read initial Soul: %v", err)
	}
	profileBefore, err := repository.GetEntry(t.Context(), "user-1", ProfilePath)
	if err != nil {
		t.Fatalf("read initial Profile: %v", err)
	}

	pngContent := avatarPNG()
	putSoul := newAvatarUploadRequest(t, "/memory/soul/avatar", "agent.png", pngContent)
	putSoul.Header.Set("X-User-Id", "user-1")
	putSoulRecorder := httptest.NewRecorder()
	handler.putAvatar(putSoulRecorder, putSoul, AvatarKindSoul)
	if putSoulRecorder.Code != http.StatusOK {
		t.Fatalf("put Soul avatar status=%d body=%s", putSoulRecorder.Code, putSoulRecorder.Body.String())
	}
	var uploaded struct {
		Code int                     `json:"code"`
		Data CurrentMemoryAvatarData `json:"data"`
	}
	if err := json.Unmarshal(putSoulRecorder.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded.Code != 0 ||
		uploaded.Data.Kind != AvatarKindSoul ||
		uploaded.Data.ContentType != "image/png" ||
		uploaded.Data.Size != int64(len(pngContent)) ||
		uploaded.Data.UpdatedAt == 0 {
		t.Fatalf("unexpected upload response: %#v", uploaded)
	}

	getSoul := httptest.NewRequest(http.MethodGet, "/memory/soul/avatar", nil)
	getSoul.Header.Set("X-User-Id", "user-1")
	getSoulRecorder := httptest.NewRecorder()
	handler.getAvatar(getSoulRecorder, getSoul, AvatarKindSoul)
	if getSoulRecorder.Code != http.StatusOK ||
		getSoulRecorder.Header().Get("Content-Type") != "image/png" ||
		getSoulRecorder.Header().Get("Cache-Control") != "private, no-store" ||
		getSoulRecorder.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!bytes.Equal(getSoulRecorder.Body.Bytes(), pngContent) {
		t.Fatalf(
			"get Soul avatar status=%d headers=%v body=%x",
			getSoulRecorder.Code,
			getSoulRecorder.Header(),
			getSoulRecorder.Body.Bytes(),
		)
	}

	otherUser := httptest.NewRequest(http.MethodGet, "/memory/soul/avatar", nil)
	otherUser.Header.Set("X-User-Id", "user-2")
	otherUserRecorder := httptest.NewRecorder()
	handler.getAvatar(otherUserRecorder, otherUser, AvatarKindSoul)
	if otherUserRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-user avatar status=%d body=%s", otherUserRecorder.Code, otherUserRecorder.Body.String())
	}

	jpegContent := avatarJPEG()
	replaceSoul := newAvatarUploadRequest(t, "/memory/soul/avatar", "agent.jpg", jpegContent)
	replaceSoul.Header.Set("X-User-Id", "user-1")
	replaceSoulRecorder := httptest.NewRecorder()
	handler.putAvatar(replaceSoulRecorder, replaceSoul, AvatarKindSoul)
	if replaceSoulRecorder.Code != http.StatusOK {
		t.Fatalf("replace avatar status=%d body=%s", replaceSoulRecorder.Code, replaceSoulRecorder.Body.String())
	}
	replaced, err := repository.GetEntry(t.Context(), "user-1", SoulAvatarPath)
	if err != nil ||
		replaced.Mime != "image/jpeg" ||
		!replaced.Binary ||
		!bytes.Equal(replaced.Content, jpegContent) {
		t.Fatalf("replaced entry=%#v err=%v", replaced, err)
	}

	webpContent := avatarWebP()
	putProfile := newAvatarUploadRequest(t, "/memory/profile/avatar", "user.webp", webpContent)
	putProfile.Header.Set("X-User-Id", "user-1")
	putProfileRecorder := httptest.NewRecorder()
	handler.putAvatar(putProfileRecorder, putProfile, AvatarKindProfile)
	if putProfileRecorder.Code != http.StatusOK {
		t.Fatalf("put Profile avatar status=%d body=%s", putProfileRecorder.Code, putProfileRecorder.Body.String())
	}
	profileAvatar, err := repository.GetEntry(t.Context(), "user-1", ProfileAvatarPath)
	if err != nil || profileAvatar.Mime != "image/webp" || !bytes.Equal(profileAvatar.Content, webpContent) {
		t.Fatalf("Profile avatar=%#v err=%v", profileAvatar, err)
	}

	soulAfter, err := repository.GetEntry(t.Context(), "user-1", SoulPath)
	if err != nil {
		t.Fatalf("read Soul after avatar update: %v", err)
	}
	profileAfter, err := repository.GetEntry(t.Context(), "user-1", ProfilePath)
	if err != nil {
		t.Fatalf("read Profile after avatar update: %v", err)
	}
	if !soulAfter.UpdatedAt.Equal(soulBefore.UpdatedAt) ||
		!bytes.Equal(soulAfter.Content, soulBefore.Content) ||
		!profileAfter.UpdatedAt.Equal(profileBefore.UpdatedAt) ||
		!bytes.Equal(profileAfter.Content, profileBefore.Content) {
		t.Fatal("avatar updates changed Soul or Profile documents")
	}

	deleteSoul := httptest.NewRequest(http.MethodDelete, "/memory/soul/avatar", nil)
	deleteSoul.Header.Set("X-User-Id", "user-1")
	deleteSoulRecorder := httptest.NewRecorder()
	handler.deleteAvatar(deleteSoulRecorder, deleteSoul, AvatarKindSoul)
	if deleteSoulRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete avatar status=%d body=%s", deleteSoulRecorder.Code, deleteSoulRecorder.Body.String())
	}
	deleteAgainRecorder := httptest.NewRecorder()
	handler.deleteAvatar(deleteAgainRecorder, deleteSoul, AvatarKindSoul)
	if deleteAgainRecorder.Code != http.StatusNoContent {
		t.Fatalf("repeat delete status=%d body=%s", deleteAgainRecorder.Code, deleteAgainRecorder.Body.String())
	}
}

func TestAvatarUploadValidation(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)

	tests := []struct {
		name       string
		request    func(*testing.T) *http.Request
		wantStatus int
	}{
		{
			name: "missing file",
			request: func(t *testing.T) *http.Request {
				t.Helper()
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				if err := writer.Close(); err != nil {
					t.Fatalf("close multipart: %v", err)
				}
				request := httptest.NewRequest(http.MethodPut, "/memory/soul/avatar", &body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
				return request
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "empty file",
			request: func(t *testing.T) *http.Request {
				return newAvatarUploadRequest(t, "/memory/soul/avatar", "empty.png", nil)
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "unsupported content",
			request: func(t *testing.T) *http.Request {
				return newAvatarUploadRequest(t, "/memory/soul/avatar", "fake.png", []byte("not an image"))
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "declared MIME mismatch",
			request: func(t *testing.T) *http.Request {
				t.Helper()
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				header := make(textproto.MIMEHeader)
				header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, "fake.png"))
				header.Set("Content-Type", "image/png")
				part, err := writer.CreatePart(header)
				if err != nil {
					t.Fatalf("create multipart file: %v", err)
				}
				if _, err := part.Write(avatarJPEG()); err != nil {
					t.Fatalf("write multipart file: %v", err)
				}
				if err := writer.Close(); err != nil {
					t.Fatalf("close multipart: %v", err)
				}
				request := httptest.NewRequest(http.MethodPut, "/memory/soul/avatar", &body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
				return request
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "too large",
			request: func(t *testing.T) *http.Request {
				content := make([]byte, AvatarMaxSize+1)
				copy(content, avatarPNG())
				return newAvatarUploadRequest(t, "/memory/soul/avatar", "large.png", content)
			},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "multiple files",
			request: func(t *testing.T) *http.Request {
				t.Helper()
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				for _, filename := range []string{"first.png", "second.png"} {
					part, err := writer.CreateFormFile("file", filename)
					if err != nil {
						t.Fatalf("create multipart file: %v", err)
					}
					if _, err := part.Write(avatarPNG()); err != nil {
						t.Fatalf("write multipart file: %v", err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatalf("close multipart: %v", err)
				}
				request := httptest.NewRequest(http.MethodPut, "/memory/soul/avatar", &body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
				return request
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := test.request(t)
			request.Header.Set("X-User-Id", "user-1")
			recorder := httptest.NewRecorder()
			handler.putAvatar(recorder, request, AvatarKindSoul)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}

	unauthenticated := newAvatarUploadRequest(t, "/memory/soul/avatar", "agent.png", avatarPNG())
	unauthenticatedRecorder := httptest.NewRecorder()
	handler.putAvatar(unauthenticatedRecorder, unauthenticated, AvatarKindSoul)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", unauthenticatedRecorder.Code, unauthenticatedRecorder.Body.String())
	}
}

func TestGetAvatarRejectsCorruptStoredResource(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	repository := NewRepository(db.DB)
	if err := repository.EnsureInitialized(t.Context(), "user-1"); err != nil {
		t.Fatalf("initialize memory: %v", err)
	}
	now := time.Now().UTC()
	entry := orm.MemoryCurrentEntry{
		UserID:    "user-1",
		Path:      SoulAvatarPath,
		EntryType: EntryFile,
		Content:   []byte("not an image"),
		Size:      int64(len("not an image")),
		Mime:      "image/png",
		FileType:  "png",
		Binary:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repository.UpsertEntry(t.Context(), entry); err != nil {
		t.Fatalf("store corrupt avatar: %v", err)
	}
	handler := NewHandler(db.DB)
	request := httptest.NewRequest(http.MethodGet, "/memory/soul/avatar", nil)
	request.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()
	handler.getAvatar(recorder, request, AvatarKindSoul)
	if recorder.Code != http.StatusInternalServerError ||
		recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func newAvatarUploadRequest(
	t *testing.T,
	target string,
	filename string,
	content []byte,
) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func avatarPNG() []byte {
	content := make([]byte, 32)
	copy(content, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	return content
}

func avatarJPEG() []byte {
	content := make([]byte, 32)
	copy(content, []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00})
	return content
}

func avatarWebP() []byte {
	content := make([]byte, 32)
	copy(content, []byte("RIFF"))
	binary.LittleEndian.PutUint32(content[4:8], uint32(len(content)-8))
	copy(content[8:], []byte("WEBPVP8 "))
	return content
}
