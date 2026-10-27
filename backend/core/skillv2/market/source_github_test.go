package market

import (
	"context"
	"testing"

	"lazymind/core/skillv2/skillpackage"
)

type githubSourceZipDownloader string

func (d githubSourceZipDownloader) Download(context.Context, string) (string, error) {
	return string(d), nil
}

func TestMarketURLSourceNormalizesSkillDocumentCase(t *testing.T) {
	archive, err := skillpackage.WriteZip(map[string][]byte{"skill.MD": []byte("skill"), "data.txt": []byte("data")}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceDeps{Downloader: githubSourceZipDownloader(archive)})
	files, err := service.filesFromSource(context.Background(), SourceInput{Type: "url", URL: "https://example.com/skill.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if string(files["SKILL.md"]) != "skill" || files["skill.MD"] != nil {
		t.Fatalf("files=%v", files)
	}
}
