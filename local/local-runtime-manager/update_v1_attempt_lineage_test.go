package main

import (
	"testing"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestV1PreparedAttemptRemembersOnlyItsImmediatePredecessor(t *testing.T) {
	dir := t.TempDir()
	actor, err := update.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(owner string, prior *update.V1ComponentHandoff) *update.V1ComponentHandoff {
		t.Helper()
		r := &update.V1ComponentHandoff{SourceID: "a", SourceVersion: "1.0.0", TargetID: "b", TargetVersion: "1.1.0", Installation: dir, Runtime: dir,
			DataProfile: "fixture", PolicyURL: "https://updates.example.invalid", SourceOwner: digestOwner(owner), SourceActor: &actor,
			EdgeDigest: hexDigest([]byte("edge")), SelectionDigest: hexDigest([]byte("selection")), SourceConfigDigest: hexDigest([]byte("config"))}
		if err := assignPreparedV1Attempt(r, prior); err != nil {
			t.Fatal(err)
		}
		if err := r.Save(dir); err != nil {
			t.Fatal(err)
		}
		got, err := update.ReadV1ComponentHandoff(dir)
		if err != nil || got == nil {
			t.Fatalf("reopen prepared receipt: %v", err)
		}
		return got
	}
	a := prepare("first", nil)
	b := prepare("second", a)
	retry := prepare("second", b)
	c := prepare("third", retry)
	if a.PreviousAttemptID != "" || b.AttemptID == a.AttemptID || b.PreviousAttemptID != a.AttemptID ||
		retry.AttemptID != b.AttemptID || retry.PreviousAttemptID != a.AttemptID ||
		c.AttemptID == b.AttemptID || c.PreviousAttemptID != b.AttemptID {
		t.Fatalf("incorrect durable predecessor after takeover, retry or another takeover: %s/%s %s/%s %s/%s",
			b.PreviousAttemptID, b.AttemptID, retry.PreviousAttemptID, retry.AttemptID, c.PreviousAttemptID, c.AttemptID)
	}
}
