package cloudbinding

import "testing"

func TestResolvePresenceIsTerminalLocalState(t *testing.T) {
	cloud := CloudResource{ID: "A", Type: "skill", Name: "A", ContentHash: "hash-a"}
	binding := Binding{CloudResourceID: "A", CloudContentHash: "hash-a", LocalResourceID: "local-a", InstalledLocalContentHash: "hash-a"}

	tests := []struct {
		name  string
		local LocalProbe
		want  PresenceStatus
	}{
		{name: "current", local: LocalProbe{Exists: true, ContentHash: "hash-a"}, want: PresencePresentCurrent},
		{name: "deleted", local: LocalProbe{Exists: false}, want: PresenceLocalMissing},
		{name: "locally modified", local: LocalProbe{Exists: true, ContentHash: "local-new"}, want: PresenceLocalModified},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolvePresence(PresenceInput{Cloud: cloud, Binding: &binding, Local: tc.local})
			if got.Status != tc.want {
				t.Fatalf("status=%q want=%q", got.Status, tc.want)
			}
		})
	}
}

func TestResolvePresenceDistinguishesCloudUpdateAndDivergence(t *testing.T) {
	binding := Binding{CloudResourceID: "A", CloudContentHash: "old", LocalResourceID: "local-a", InstalledLocalContentHash: "old"}
	tests := []struct {
		name      string
		cloudHash string
		localHash string
		want      PresenceStatus
	}{
		{name: "cloud only", cloudHash: "cloud-new", localHash: "old", want: PresenceCloudUpdated},
		{name: "both", cloudHash: "cloud-new", localHash: "local-new", want: PresenceDiverged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolvePresence(PresenceInput{
				Cloud:   CloudResource{ID: "A", Type: "skill", Name: "A", ContentHash: tc.cloudHash},
				Binding: &binding,
				Local:   LocalProbe{Exists: true, ContentHash: tc.localHash},
			})
			if got.Status != tc.want {
				t.Fatalf("status=%q want=%q", got.Status, tc.want)
			}
		})
	}
}

func TestResolvePresenceIsIndependentPerTerminal(t *testing.T) {
	resources := []CloudResource{
		{ID: "A", Type: "skill", Name: "A", ContentHash: "a"},
		{ID: "B", Type: "skill", Name: "B", ContentHash: "b"},
		{ID: "C", Type: "workflow", Name: "C", ContentHash: "c"},
		{ID: "D", Type: "workflow", Name: "D", ContentHash: "d"},
	}
	terminalA := map[string]bool{"A": true, "C": true}
	terminalB := map[string]bool{"A": true, "B": true, "D": true}

	assertTerminal := func(name string, installed map[string]bool) {
		t.Helper()
		for _, resource := range resources {
			var binding *Binding
			local := LocalProbe{}
			if installed[resource.ID] {
				binding = &Binding{CloudResourceID: resource.ID, CloudContentHash: resource.ContentHash, LocalResourceID: "local-" + resource.ID, InstalledLocalContentHash: resource.ContentHash}
				local = LocalProbe{Exists: true, ContentHash: resource.ContentHash}
			}
			got := ResolvePresence(PresenceInput{Cloud: resource, Binding: binding, Local: local})
			want := PresenceDownloadRequired
			if installed[resource.ID] {
				want = PresencePresentCurrent
			}
			if got.Status != want {
				t.Fatalf("%s resource=%s status=%q want=%q", name, resource.ID, got.Status, want)
			}
		}
	}
	assertTerminal("terminal-a", terminalA)
	assertTerminal("terminal-b", terminalB)
}

func TestResolveUploadAvoidsDuplicateTransfer(t *testing.T) {
	binding := Binding{CloudResourceID: "A", CloudContentHash: "same", LocalResourceID: "local-a", InstalledLocalContentHash: "same"}
	tests := []struct {
		name      string
		cloudHash string
		localHash string
		want      UploadStatus
		wantZIP   bool
		wantAPI   bool
	}{
		{name: "unchanged", cloudHash: "same", localHash: "same", want: UploadNotRequired},
		{name: "local changed", cloudHash: "same", localHash: "local-new", want: UploadUpdateAvailable, wantZIP: true, wantAPI: true},
		{name: "cloud changed", cloudHash: "cloud-new", localHash: "same", want: UploadCloudUpdated},
		{name: "diverged", cloudHash: "cloud-new", localHash: "local-new", want: UploadDiverged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveUpload(UploadInput{
				Cloud:     &CloudResource{ID: "A", Type: "skill", Name: "A", ContentHash: tc.cloudHash},
				Binding:   &binding,
				Local:     LocalProbe{Exists: true, ContentHash: tc.localHash},
				LocalName: "A",
			})
			if got.Status != tc.want || got.CreateZIP != tc.wantZIP || got.CallUpsert != tc.wantAPI {
				t.Fatalf("decision=%+v want status=%q zip=%v api=%v", got, tc.want, tc.wantZIP, tc.wantAPI)
			}
		})
	}
}

func TestResolveUploadAdoptsUniqueSameNameSameHashCandidate(t *testing.T) {
	got := ResolveUpload(UploadInput{
		Local:     LocalProbe{Exists: true, ContentHash: "same"},
		LocalName: "A",
		Candidates: []CloudResource{
			{ID: "A-cloud", Type: "skill", Name: "A", ContentHash: "same"},
		},
	})
	if got.Status != UploadNotRequired || got.AdoptResourceID != "A-cloud" || got.CreateZIP || got.CallUpsert {
		t.Fatalf("unexpected adoption decision: %+v", got)
	}
}
