package credentialvault

import (
	"context"
	"testing"
)

func TestRestoreGroupsOldAndActiveKeyVersionsIntoSeparateCloudOperations(t *testing.T) {
	service, cloud, sink, command := newRestoreServiceFixture(t, 3, RestoreTrustedDevice)
	cloud.records[0].KeyID = "credential-shard-17-v1"
	cloud.records[1].KeyID = "credential-shard-17-v2"
	cloud.records[2].KeyID = "credential-shard-17-v1"

	started, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("start mixed-version restore: %v", err)
	}
	if len(cloud.createdBatches) != 1 || len(cloud.createdBatches[0]) != 2 {
		t.Fatalf("first key-version batch = %+v, want two v1 records", cloud.createdBatches)
	}
	operation := started
	for attempts := 0; attempts < 3 && operation.Status != "succeeded"; attempts++ {
		operation, err = service.Advance(context.Background(), operation.OperationID)
		if err != nil {
			t.Fatalf("advance mixed-version restore: %v", err)
		}
	}
	if operation.Status != "succeeded" || len(cloud.createdBatches) != 2 || len(cloud.createdBatches[1]) != 1 || len(sink.trusted) != 3 {
		t.Fatalf("mixed-version restore = %+v batches=%+v persisted=%d", operation, cloud.createdBatches, len(sink.trusted))
	}
}
