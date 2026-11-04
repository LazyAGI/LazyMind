package academic

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

func TestZoteroAttachmentKeepsSourceWhenMatchingExistingPaper(t *testing.T) {
	for _, source := range []string{"local", "arxiv", "doi"} {
		t.Run(source, func(t *testing.T) {
			db, path := zoteroStorageFixture(t)
			original := libraryDraft("existing-paper", "My corrected bibliography", "10.1234/mixed-source")
			original.Source = source
			importDraft(t, "papers", "existing-paper", original)
			incoming := libraryDraft("zotero-paper", "Provider bibliography", "10.1234/mixed-source")
			incoming.Source = "zotero"
			graph := paperlibrary.EmptyData()
			graph.Papers = []paperlibrary.Paper{*incoming}
			graph.Attachments = []paperlibrary.Attachment{{ID: "mixed-source-attachment", PaperID: incoming.ID, Name: "article.pdf", Kind: "pdf", Source: "zotero", AddedAt: time.Now().UTC()}}
			if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", marshal(graph)); err != nil {
				t.Fatal(err)
			}
			view, err := paperlibrary.Load(t.Context(), db, "papers", "owner", []string{original.ID}, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Data.Papers) != 1 || view.Data.Papers[0].Source != source || view.Data.Papers[0].Metadata.Title != original.Metadata.Title {
				t.Fatal("existing bibliography changed", view.Data.Papers)
			}
			if len(view.Data.Attachments) != 1 || view.Data.Attachments[0].Source != "zotero" || view.Data.Attachments[0].PaperID != original.ID {
				t.Fatal("attachment provenance replaced by paper provenance", view.Data.Attachments)
			}
			var roundTrip paperlibrary.Data
			if err = json.Unmarshal(marshal(view.Data), &roundTrip); err != nil || roundTrip.Attachments[0].Source != "zotero" {
				t.Fatal("JSON projection lost attachment source", roundTrip, err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := storeZoteroFile(t.Context(), db, "papers", original.ID, "mixed-source-attachment", path, "article.pdf", "owner", "Owner")
				if err != nil {
					t.Fatal("matched Zotero attachment could not be stored", err)
				}
				found := false
				for _, attachment := range result.Data.Attachments {
					if attachment.ID == "mixed-source-attachment" {
						found = true
						if attachment.Source != "zotero" || attachment.ContentRef == "" || !attachment.Available {
							t.Fatal("stored attachment lost source or original", attachment)
						}
					}
				}
				if !found {
					t.Fatal("stored attachment absent")
				}
			}
			var documents int64
			if err = db.Model(&orm.Document{}).Count(&documents).Error; err != nil || documents != 1 {
				t.Fatal("upload replay duplicated original", documents, err)
			}
		})
	}
}
