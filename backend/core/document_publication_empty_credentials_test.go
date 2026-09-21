package main

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestDocumentPublicationEmptyCloudCredentialsStopsBeforeWrite(t *testing.T) {
	for _, provider := range []string{"feishu", "github", "googledrive", "notion", "wechat"} {
		t.Run(provider, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			s.provider = provider
			s.target["adapter"] = provider
			s.result["provider"] = provider
			s.emptyCredentials = true
			body := deliveryBody("no-cloud-credentials")
			body["input"].(map[string]any)["provider"] = provider
			before := descriptorSnapshot(t, f)
			w := deliveryPublish(t, f, body)
			rewriteError(t, w, 502, "PROVIDER_CREDENTIALS_UNAVAILABLE")
			if len(s.allCalls()) != 0 {
				t.Fatal("empty authorization reached conversion/write")
			}
			if descriptorSnapshot(t, f) != before {
				t.Fatal("authorization failure changed document")
			}
			var op orm.DocumentPublicationOperation
			if err := f.db.Where("idempotency_key = ?", "no-cloud-credentials").First(&op).Error; err != nil {
				t.Fatal(err)
			}
			if op.Status != "failed_no_write" || op.ErrorCode != "PROVIDER_CREDENTIALS_UNAVAILABLE" {
				t.Fatalf("unexpected operation %s/%s", op.Status, op.ErrorCode)
			}
			var pending int64
			if err := f.db.Model(&orm.DocumentPublicationBinding{}).Where("pending_operation_id = ?", op.ID).Count(&pending).Error; err != nil {
				t.Fatal(err)
			}
			if pending != 0 {
				t.Fatal("authorization failure retained write reservation")
			}
			s.emptyCredentials = false
			body["input"].(map[string]any)["idempotency_key"] = "authorized-retry"
			data := rewriteData(t, deliveryPublish(t, f, body))
			if data["provider_synced"] != true || len(s.writes()) != 1 {
				t.Fatal("authorization recovery did not publish exactly once")
			}
		})
	}
}

func TestDocumentPublicationEmptyCredentialsReleasesSharedWriterTarget(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	s.provider = "feishu"
	s.emptyCredentials = true
	for _, update := range []struct {
		model  any
		id     string
		values any
	}{
		{&orm.WorkflowSession{}, "descriptor-session", &orm.WorkflowSession{WorkflowID: "writer-workflow"}},
		{&orm.WorkflowSlotRevision{}, "descriptor-artifact", &orm.WorkflowSlotRevision{SlotID: "flat_draft_document", Slot: "flat_draft_document"}},
	} {
		if err := f.db.Model(update.model).Where("id = ?", update.id).Updates(update.values).Error; err != nil {
			t.Fatal(err)
		}
	}
	body := deliveryBody("empty-shared-target")
	body["input"].(map[string]any)["provider"] = "feishu"
	rewriteError(t, deliveryPublish(t, f, body), 502, "PROVIDER_CREDENTIALS_UNAVAILABLE")
	var op orm.DocumentPublicationOperation
	if err := f.db.Where("idempotency_key = ?", "empty-shared-target").First(&op).Error; err != nil {
		t.Fatal(err)
	}
	if !op.SharedTarget || op.Status != "failed_no_write" {
		t.Fatalf("unexpected shared operation: %s", op.Status)
	}
	var bindings []orm.DocumentPublicationBinding
	if err := f.db.Where("session_id = ?", op.SessionID).Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 || len(s.allCalls()) != 0 {
		t.Fatal("missing authorization retained shared target or invoked algorithm action")
	}
}
