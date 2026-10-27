package handler

import (
	"context"
	"os"
	"testing"
	"time"

	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

// These opt-in tests exercise the actual public website, metadata resolution,
// package download, validation, blob persistence and original-source revision.
// Network-independent fixtures cover these contracts in the normal test suite.
func TestLiveSkillWebsiteImports(t *testing.T) {
	if os.Getenv("LAZYMIND_LIVE_SKILL_IMPORT_TESTS") != "1" {
		t.Skip("set LAZYMIND_LIVE_SKILL_IMPORT_TESTS=1 to verify public website imports")
	}
	for _, tt := range []struct{ site, source string }{
		{"skills_sh", "https://skills.sh/vercel-labs/agent-skills/vercel-react-best-practices?utm_source=share#install"},
		{"skills_sh_www", "https://www.skills.sh/vercel-labs/agent-skills/vercel-react-best-practices?utm_source=share#install"},
		{"skillsmp", "https://skillsmp.com/skills/anthropics-skills-skills-frontend-design-skill-md?utm_source=share#install"},
		{"skillsmp_www", "https://www.skillsmp.com/skills/anthropics-skills-skills-frontend-design-skill-md?utm_source=share#install"},
		{"clawhub", "https://clawhub.ai/paudyyin/skills/summarize?version=1.0.0#install"},
		{"clawhub_www_legacy", "https://www.clawhub.ai/paudyyin/summarize?tag=latest#install"},
		{"skillhub", "https://skillhub.cn/skills/clawhub_paudyyin/summarize?version=1.0.0#install"},
		{"skillhub_www", "https://www.skillhub.cn/skills/clawhub_paudyyin/summarize?version=1.0.0#install"},
		{"github_directory", "https://github.com/anthropics/skills/tree/main/skills/frontend-design?tab=readme#install"},
		{"github_skill_md", "https://github.com/anthropics/skills/blob/main/skills/frontend-design/SKILL.md?plain=1#install"},
		{"clawhub_direct_zip", "https://clawhub.ai/api/v1/download?owner=paudyyin&slug=summarize&version=1.0.0"},
	} {
		t.Run(tt.site, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			source, err := (skillSourceRequest{Type: "url", URL: tt.source}).toServiceSource(ctx)
			if err != nil {
				t.Fatal(err)
			}
			db := testutil.NewTestDB(t)
			svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
				DB: db.DB, Downloader: skillservice.HTTPZipDownloader{},
				BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
			})
			response, err := svc.CreateSkill(ctx, skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source})
			if err != nil {
				t.Fatal(err)
			}
			var revision testutil.SkillRevisionRow
			if err := db.Where("id = ?", response.HeadRevisionID).Take(&revision).Error; err != nil {
				t.Fatal(err)
			}
			if revision.SourceRefID != tt.source {
				t.Fatalf("original website URL was lost: %q", revision.SourceRefID)
			}
			if count := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path = ?", response.HeadRevisionID, "SKILL.md"); count != 1 {
				t.Fatalf("expected a persisted SKILL.md, got %d", count)
			}
			t.Logf("imported %s: name=%s", tt.site, response.SkillName)
		})
	}
}
