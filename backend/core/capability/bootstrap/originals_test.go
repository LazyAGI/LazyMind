package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"lazymind/core/artifact"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkOriginalResolverPinsPublishedRevisionAndChecksOwner(t *testing.T) {
	db := orm.OpenTestDB(t)
	if err := db.AutoMigrate(&orm.ArtifactV2{}, &orm.ArtifactHead{}, &orm.ArtifactRevision{}, &orm.ArtifactBlob{}, &orm.UploadedFile{}); err != nil {
		t.Fatal(err)
	}
	blobRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ARTIFACT_STORAGE_ROOT", blobRoot)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	blob, err := artifact.PutBlob("tenant", "text/plain", strings.NewReader("published exact bytes"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.ArtifactV2{ID: "artifact", OwnerUserID: "owner", TenantID: "tenant", Kind: "file", Title: "result.txt", Status: "active", CreatedAt: now, UpdatedAt: now},
		&orm.ArtifactBlob{ID: "blob", TenantID: "tenant", SHA256: blob.SHA256, Size: blob.Size, MIMEType: "text/plain", StorageBackend: "local", StorageKey: blob.StorageKey, State: "ready", CreatedAt: now},
		&orm.ArtifactRevision{ID: "revision", ArtifactID: "artifact", RevisionNo: 1, BlobID: "blob", ContentType: "text/plain", ContentHash: "sha256:" + blob.SHA256, Size: blob.Size, Metadata: []byte(`{}`), CreatedAt: now},
		&orm.ArtifactHead{ArtifactID: "artifact", Channel: "published", RevisionID: "revision", Version: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc, err := newCapabilityOriginalService(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	caller := doc.DatasetCatalogCaller{UserID: "owner", TenantID: "tenant"}
	ref, err := svc.AcquireWorkOriginal(context.Background(), caller, "artifact", "")
	if err != nil {
		t.Fatal(err)
	}
	if ref.RevisionID != "revision" || ref.ContentSHA256 != blob.SHA256 {
		t.Fatalf("unfixed ref: %+v", ref)
	}
	for _, other := range []doc.DatasetCatalogCaller{{UserID: "other", TenantID: "tenant"}, {UserID: "owner", TenantID: "other"}} {
		if _, err := svc.AcquireWorkOriginal(context.Background(), other, "artifact", "revision"); err == nil {
			t.Fatal("foreign artifact accepted")
		}
	}
}

func TestWorkOriginalResolverUsesRealDualWritersOwnerNamespaceAndBlobMIME(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ArtifactV2{}, &orm.ArtifactBlob{}, &orm.ArtifactRevision{}, &orm.ArtifactHead{}, &orm.ArtifactBinding{}, &orm.ArtifactDependency{}, &orm.ArtifactIdempotency{}, &orm.ArtifactEventOutbox{}, &orm.UploadedFile{})
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	blobRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ARTIFACT_STORAGE_ROOT", blobRoot)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	workspace := t.TempDir()
	file := filepath.Join(workspace, "report.pdf")
	bytes := []byte("%PDF-1.4\nexact generated pdf bytes\n%%EOF")
	if err := os.WriteFile(file, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]string{"path": file})
	ctx := context.Background()
	ownerSvc := artifact.New(db.DB)
	if err := artifact.DualWriteMainChat(ctx, ownerSvc, "conversation", "history", "owner", artifact.MainChatWrite{}, orm.ConversationArtifact{ID: "main-file", Filename: "report.pdf", ContentType: "file", Value: value}); err != nil {
		t.Fatal(err)
	}
	binding, err := ownerSvc.FindByLegacyID(ctx, "main-file")
	if err != nil {
		t.Fatal(err)
	}
	mainID := binding.ArtifactID
	sub, err := artifact.DualWriteSubAgent(ctx, ownerSvc, artifact.SubAgentSnapshot{TaskID: "task", OwnerUserID: "owner", WorkspacePath: workspace}, artifact.SubAgentLegacyArtifact{ID: "sub-file", Slot: "report", ContentType: "file", Value: json.RawMessage(`{"path":"report.pdf"}`)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := newCapabilityOriginalService(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{mainID, sub.ArtifactID} {
		ref, err := svc.AcquireWorkOriginal(ctx, doc.DatasetCatalogCaller{UserID: "owner", TenantID: "default"}, id, "")
		if err != nil {
			t.Fatalf("real owner import %s: %v", id, err)
		}
		sum := sha256.Sum256(bytes)
		if ref.ContentType != "application/pdf" || ref.ContentSHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("wrong original %+v", ref)
		}
		if _, err := svc.AcquireWorkOriginal(ctx, doc.DatasetCatalogCaller{UserID: "intruder", TenantID: "owner"}, id, ""); err == nil {
			t.Fatal("owner namespace became a cross-user grant")
		}
		if _, err := svc.ResolveOriginal(ctx, doc.DatasetCatalogCaller{UserID: "owner", TenantID: "other-tenant"}, ref.FileRef); err == nil {
			t.Fatal("acquired original crossed its target tenant")
		}
	}
	for _, kind := range []string{"text", "json"} {
		payload := json.RawMessage(`{"text":"preserve inline revision bytes"}`)
		row := orm.ConversationArtifact{ID: "main-" + kind, Filename: "inline", ContentType: kind, Value: payload}
		if err := artifact.DualWriteMainChat(ctx, ownerSvc, "conversation", "history", "owner", artifact.MainChatWrite{}, row); err != nil {
			t.Fatal(err)
		}
		bound, err := ownerSvc.FindByLegacyID(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := svc.AcquireWorkOriginal(ctx, doc.DatasetCatalogCaller{UserID: "owner", TenantID: "default"}, bound.ArtifactID, "")
		if err != nil || ref.ContentType != "application/json" {
			t.Fatalf("inline %s: %+v %v", kind, ref, err)
		}
		sum := sha256.Sum256(payload)
		if ref.ContentSHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("inline revision bytes changed")
		}
		sub, err := artifact.DualWriteSubAgent(ctx, ownerSvc, artifact.SubAgentSnapshot{TaskID: "inline-task", OwnerUserID: "owner", WorkspacePath: workspace}, artifact.SubAgentLegacyArtifact{ID: "sub-" + kind, Slot: "inline", ContentType: kind, Value: payload})
		if err != nil {
			t.Fatal(err)
		}
		subRef, err := svc.AcquireWorkOriginal(ctx, doc.DatasetCatalogCaller{UserID: "owner", TenantID: "default"}, sub.ArtifactID, sub.RevisionID)
		if err != nil || subRef.ContentSHA256 != ref.ContentSHA256 || subRef.ContentType != "application/json" {
			t.Fatalf("subagent inline %s: %+v %v", kind, subRef, err)
		}
	}
}

func TestWorkOriginalResolverErrors(t *testing.T) {
	cases := []struct{ name, artifactID, revisionID, user, tenant, want, drop string }{
		{name: "missing artifact", artifactID: "missing", revisionID: "missing", want: "NOT_FOUND"},
		{name: "missing revision", artifactID: "artifact", revisionID: "missing", want: "NOT_FOUND"},
		{name: "wrong owner", artifactID: "artifact", revisionID: "published", user: "intruder", want: "NOT_FOUND"},
		{name: "wrong tenant", artifactID: "artifact", revisionID: "published", tenant: "foreign", want: "NOT_FOUND"},
		{name: "wrong artifact", artifactID: "other", revisionID: "published", want: "NOT_FOUND"},
		{name: "future draft", artifactID: "artifact", revisionID: "draft", want: "INVALID_ARGUMENT"},
		{name: "never published", artifactID: "draft-only", revisionID: "unpublished", want: "INVALID_ARGUMENT"},
		{name: "head database failure", artifactID: "artifact", revisionID: "published", drop: "artifact_heads", want: "INTERNAL"},
		{name: "owner database failure", artifactID: "artifact", revisionID: "published", drop: "artifacts", want: "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.Dataset{}, &orm.ArtifactV2{}, &orm.ArtifactHead{}, &orm.ArtifactRevision{}, &orm.ArtifactBlob{}, &orm.UploadedFile{})
			user, tenant := tc.user, tc.tenant
			if user == "" {
				user = "owner"
			}
			if tenant == "" {
				tenant = "tenant"
			}
			for _, row := range []any{
				&orm.Dataset{ID: "target", TenantID: tenant, BaseModel: orm.BaseModel{CreateUserID: user}},
				&orm.ArtifactV2{ID: "artifact", OwnerUserID: "owner", TenantID: "tenant", Status: "active"},
				&orm.ArtifactV2{ID: "draft-only", OwnerUserID: "owner", TenantID: "tenant", Status: "active"},
				&orm.ArtifactRevision{ID: "published", ArtifactID: "artifact", RevisionNo: 1, Metadata: []byte(`{}`)},
				&orm.ArtifactRevision{ID: "draft", ArtifactID: "artifact", RevisionNo: 2, Metadata: []byte(`{}`)},
				&orm.ArtifactRevision{ID: "unpublished", ArtifactID: "draft-only", RevisionNo: 1, Metadata: []byte(`{}`)},
				&orm.ArtifactHead{ArtifactID: "artifact", Channel: "published", RevisionID: "published", Version: 1},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			scan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"items":[{"dataset_id":"target","exists":false,"allowed":true}]}`)
			}))
			defer scan.Close()
			t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", scan.URL)
			if tc.drop != "" {
				if err := db.Migrator().DropTable(tc.drop); err != nil {
					t.Fatal(err)
				}
			}
			originals, err := newCapabilityOriginalService(db.DB)
			if err != nil {
				t.Fatal(err)
			}
			_, err = originals.AcquireWorkOriginal(context.Background(), doc.DatasetCatalogCaller{UserID: user, TenantID: tenant}, tc.artifactID, tc.revisionID)
			var typed *doc.ResourceError
			if err == nil || (tc.want != "INTERNAL" && (!errors.As(err, &typed) || typed.Code != tc.want)) {
				t.Fatalf("original error=%+v want=%s", err, tc.want)
			}

		})
	}
}
