package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bumped is the auggie fixture entry at a newer pinned version, the way the
// hourly automation would republish it.
func bumped(t *testing.T) Entry {
	t.Helper()
	e := fixtureEntry(t, "auggie")
	e.Version = "0.37.0"
	e.Distribution.Npx.Package = "@augmentcode/auggie@0.37.0"
	return e
}

func TestPlanUpdate_SwapsTheNewBuildIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeNpm(t, fakeNpmSuccess)

	plan, err := NewPlan(fixtureEntry(t, "auggie"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	up, err := PlanUpdate(bumped(t), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	// The staged build must not run under the live name, and the consent
	// body must say where it really runs.
	if filepath.Base(up.Dir) != ".update-auggie" {
		t.Errorf("staging dir = %q", up.Dir)
	}
	if !strings.Contains(up.Describe(), ".update-auggie") {
		t.Error("Describe() hides the staging path the command really uses")
	}

	rec, err := up.Run(context.Background())
	if err != nil {
		t.Fatalf("update Run: %v", err)
	}
	final := filepath.Join(agentsDir(), "auggie")
	if rec.Dir != final || !strings.HasPrefix(rec.Command, final+string(os.PathSeparator)) {
		t.Errorf("record = %+v, want it rehomed under %s", rec, final)
	}
	if _, err := os.Stat(rec.Command); err != nil {
		t.Errorf("the updated command is not there: %v", err)
	}

	records, problems := Installed()
	if len(records) != 1 || len(problems) != 0 {
		t.Fatalf("store after update = %d records, %v", len(records), problems)
	}
	if records[0].Entry.Version != "0.37.0" {
		t.Errorf("version = %s, want 0.37.0", records[0].Entry.Version)
	}
	// No stage and no stepped-aside copy may survive the swap.
	entries, err := os.ReadDir(agentsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "auggie" {
			t.Errorf("leftover %q in the store", e.Name())
		}
	}
}

func TestPlanUpdate_AFailedBuildLeavesTheOldInstallWorking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeNpm(t, fakeNpmSuccess)
	plan, err := NewPlan(fixtureEntry(t, "auggie"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := plan.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	fakeNpm(t, "exit 1")
	up, err := PlanUpdate(bumped(t), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.Run(context.Background()); err == nil {
		t.Fatal("a failing update reported success")
	}

	records, problems := Installed()
	if len(records) != 1 || len(problems) != 0 {
		t.Fatalf("store after failed update = %d records, %v", len(records), problems)
	}
	if records[0].Entry.Version != "0.36.0" {
		t.Errorf("version = %s, want the old 0.36.0 untouched", records[0].Entry.Version)
	}
	if _, err := os.Stat(installed.Command); err != nil {
		t.Errorf("the old command is gone: %v", err)
	}
}

// An update where nothing was installed before still lands — the swap has
// nothing to step aside, which must not read as failure.
func TestPlanUpdate_WorksWithNoPriorInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeNpm(t, fakeNpmSuccess)
	up, err := PlanUpdate(bumped(t), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := up.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(rec.Command); err != nil {
		t.Error("the install did not land")
	}
}
