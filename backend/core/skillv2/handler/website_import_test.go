package handler

import (
	"context"
	"os"
	"strings"
	"testing"

	skillservice "lazymind/core/skillv2/service"
	skillpackage "lazymind/core/skillv2/skillpackage"
	"lazymind/core/skillv2/testutil"
)

func TestWebsiteAdaptersWorkForPersonalImportAndAdminPublish(t *testing.T) {
	const document = "---\nname: demo\ndescription: Website adapter fixture.\n---\n# Demo\n"
	for _, site := range []string{"skills_sh", "skillsmp", "clawhub", "skillhub"} {
		t.Run(site, func(t *testing.T) {
			sourceURL := "https://" + site + ".com/creators/acme/repo/skills-demo?utm_source=share#install"
			routes := map[string]any{}
			if site == "skillsmp" || site == "skills_sh" {
				source := "https://github.com/acme/repo/tree/" + marketGitHubCommit + "/skills/demo"
				if site == "skillsmp" {
					routes["skillsmp.com/creators/acme/repo/skills-demo"] = `<script type="application/ld+json">{"@type":"SoftwareSourceCode","codeRepository":"` + source + `"}</script>`
				} else {
					sourceURL = "https://skills.sh/acme/repo/demo?utm_source=share#install"
					routes["www.skills.sh/acme/repo/demo"] = `<a href="` + source + `">Source</a>`
				}
				api := "api.github.com/repos/acme/repo/git/trees/"
				routes[api+marketGitHubCommit] = marketGitHubTree(marketGitHubEntry("skills", "040000", "tree", "skills-tree", 0))
				routes[api+"skills-tree"] = marketGitHubTree(marketGitHubEntry("demo", "040000", "tree", "demo-tree", 0))
				routes[api+"demo-tree?recursive=1"] = marketGitHubTree(marketGitHubEntry("SKILL.md", "100644", "blob", "doc", int64(len(document))), marketGitHubEntry("assets/logo.png", "100644", "blob", "image", 3))
				routes["raw.githubusercontent.com/acme/repo/"+marketGitHubCommit+"/skills/demo/SKILL.md"] = document
				routes["raw.githubusercontent.com/acme/repo/"+marketGitHubCommit+"/skills/demo/assets/logo.png"] = []byte{0x89, 0x00, 0xff}
			} else {
				archive := marketGitHubArchive(t, map[string]string{"SKILL.md": document, "assets/logo.png": "\x89\x00\xff"})
				if site == "clawhub" {
					sourceURL = "https://clawhub.ai/acme/skills/demo?version=1.2.3#install"
					routes["clawhub.ai/api/v1/download?owner=acme&slug=demo&version=1.2.3"] = archive
				} else {
					sourceURL = "https://skillhub.cn/skills/acme/demo?version=1.2.3#install"
					routes["api.skillhub.cn/api/v1/download?slug=%40acme%2Fdemo&version=1.2.3"] = archive
				}
			}
			withMarketGitHubHTTPFixture(t, routes)
			source, err := (skillSourceRequest{Type: "url", URL: sourceURL}).toServiceSource(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			db := testutil.NewTestDB(t)
			svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB, Downloader: httpZipDownloader{}, BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir()))})
			result, err := svc.CreateSkill(context.Background(), skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source})
			if err != nil {
				t.Fatal(err)
			}
			var revision testutil.SkillRevisionRow
			if err := db.Where("id = ?", result.HeadRevisionID).Take(&revision).Error; err != nil || revision.SourceRefID != sourceURL {
				t.Fatalf("original website provenance missing: %#v, %v", revision, err)
			}
			if count := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path = ?", result.HeadRevisionID, "assets/logo.png"); count != 1 {
				t.Fatal("binary asset was not persisted")
			}
			archive, err := (marketHTTPZipDownloader{}).Download(context.Background(), sourceURL)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archive)
			pkg, err := skillpackage.ReadZip(archive)
			if err != nil || string(pkg.Files["assets/logo.png"]) != "\x89\x00\xff" || !strings.Contains(string(pkg.Files["SKILL.md"]), "Website adapter fixture") {
				t.Fatalf("admin import lost original files: %#v, %v", pkg, err)
			}
		})
	}
}
