package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckDreamGate_FirstRun_TooFewNew(t *testing.T) {
	workDir := t.TempDir()
	cfg := DreamConfig{MinSinceLast: time.Hour, MinNewEntries: 5}

	g, err := CheckDreamGate(workDir, cfg)
	if err != nil {
		t.Fatalf("CheckDreamGate: %v", err)
	}
	if g.Ready {
		t.Error("fresh workspace should not be ready — zero entries")
	}
	if g.Reason != ReasonTooFewNew {
		t.Errorf("expected too-few-new, got %q", g.Reason)
	}
}

func TestCheckDreamGate_FirstRun_Ready(t *testing.T) {
	workDir := t.TempDir()
	cfg := DreamConfig{MinSinceLast: time.Hour, MinNewEntries: 2, StaleLockAge: time.Minute}

	// Seed 3 entries — over the minimum.
	for i, name := range []string{"a", "b", "c"} {
		_, err := WriteEntry(workDir, name, Entry{
			Name: "entry " + string(rune('A'+i)), Description: "d",
			Type: TypeProject, Body: "body",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	g, err := CheckDreamGate(workDir, cfg)
	if err != nil {
		t.Fatalf("CheckDreamGate: %v", err)
	}
	if !g.Ready {
		t.Errorf("expected ready, got %+v", g)
	}
	if g.NewEntries != 3 {
		t.Errorf("expected 3 new entries, got %d", g.NewEntries)
	}
}

func TestCheckDreamGate_TooSoon(t *testing.T) {
	workDir := t.TempDir()
	cfg := DreamConfig{MinSinceLast: 24 * time.Hour, MinNewEntries: 1}

	// Mark consolidated recently.
	if err := MarkConsolidated(workDir); err != nil {
		t.Fatal(err)
	}
	// Add some entries after the mark.
	_, _ = WriteEntry(workDir, "a", Entry{Name: "A", Description: "d", Type: TypeUser, Body: "b"})
	_, _ = WriteEntry(workDir, "b", Entry{Name: "B", Description: "d", Type: TypeUser, Body: "b"})

	g, err := CheckDreamGate(workDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g.Ready {
		t.Error("should not be ready immediately after MarkConsolidated")
	}
	if g.Reason != ReasonTooSoon {
		t.Errorf("expected too-soon, got %q", g.Reason)
	}
}

func TestCheckDreamGate_LockedGate(t *testing.T) {
	workDir := t.TempDir()
	cfg := DreamConfig{MinSinceLast: time.Nanosecond, MinNewEntries: 1, StaleLockAge: time.Hour}

	// Seed one entry so the count gate passes.
	_, _ = WriteEntry(workDir, "a", Entry{Name: "A", Description: "d", Type: TypeUser, Body: "b"})

	release, err := TryAcquireDreamLock(workDir, cfg)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer release()

	g, err := CheckDreamGate(workDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g.Ready {
		t.Error("should not be ready while lock is held")
	}
	if g.Reason != ReasonLocked {
		t.Errorf("expected locked, got %q", g.Reason)
	}
}

func TestTryAcquireDreamLock_Exclusive(t *testing.T) {
	workDir := t.TempDir()
	cfg := DreamConfig{StaleLockAge: time.Hour}

	release, err := TryAcquireDreamLock(workDir, cfg)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Second acquire without release must fail.
	_, err = TryAcquireDreamLock(workDir, cfg)
	if err == nil {
		t.Error("second acquire should fail while lock is held")
	}

	release()

	// After release, next acquire should succeed again.
	release2, err := TryAcquireDreamLock(workDir, cfg)
	if err != nil {
		t.Fatalf("post-release acquire: %v", err)
	}
	release2()
}

func TestTryAcquireDreamLock_StaleEviction(t *testing.T) {
	workDir := t.TempDir()
	if err := Ensure(workDir); err != nil {
		t.Fatal(err)
	}

	// Drop a lock file, backdate its mtime to 1 hour ago.
	lockPath := dreamLockPath(workDir)
	if err := os.WriteFile(lockPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	// With StaleLockAge=10m the old lock is evicted and we can acquire.
	release, err := TryAcquireDreamLock(workDir, DreamConfig{StaleLockAge: 10 * time.Minute})
	if err != nil {
		t.Fatalf("stale eviction failed: %v", err)
	}
	release()
}

func TestApplyConsolidation_PreservesScaffolds(t *testing.T) {
	workDir := t.TempDir()
	if err := Ensure(workDir); err != nil {
		t.Fatal(err)
	}

	// Scaffold file (no type prefix) — must survive consolidation.
	scaffold := filepath.Join(MemoryDir(workDir), "architecture.md")
	if err := os.WriteFile(scaffold, []byte("# Architecture\n\nHand-written.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed two auto entries that will be replaced by consolidation.
	_, _ = WriteEntry(workDir, "old-a", Entry{Name: "Old A", Description: "d", Type: TypeProject, Body: "stale"})
	_, _ = WriteEntry(workDir, "old-b", Entry{Name: "Old B", Description: "d", Type: TypeProject, Body: "stale"})

	consolidated := []Entry{
		{Name: "Merged Project", Description: "single merged entry", Type: TypeProject, Body: "clarified"},
	}
	if err := ApplyConsolidation(workDir, consolidated); err != nil {
		t.Fatalf("ApplyConsolidation: %v", err)
	}

	// Scaffold still there.
	if _, err := os.Stat(scaffold); err != nil {
		t.Errorf("scaffold must survive consolidation: %v", err)
	}

	// Old auto files gone.
	dir := MemoryDir(workDir)
	for _, name := range []string{"project_old-a.md", "project_old-b.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("auto file %s should have been removed, err=%v", name, err)
		}
	}

	// New consolidated entry present, and scan returns exactly 1.
	scanned, err := Scan(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 1 || !strings.Contains(scanned[0].Body, "clarified") {
		t.Errorf("expected exactly 1 consolidated entry, got %+v", scanned)
	}

	// Dream state should have been bumped.
	state, err := readDreamState(workDir)
	if err != nil {
		t.Fatalf("dream state not written: %v", err)
	}
	if state.LastConsolidatedUnix == 0 {
		t.Error("LastConsolidatedUnix should be non-zero after ApplyConsolidation")
	}
	if state.LastEntryCount != 1 {
		t.Errorf("LastEntryCount should be 1, got %d", state.LastEntryCount)
	}
}

func TestIsAutoEntryFilename(t *testing.T) {
	cases := map[string]bool{
		"user_role.md":         true,
		"feedback_db-rule.md":  true,
		"project_deadline.md":  true,
		"reference_grafana.md": true,
		"architecture.md":      false,
		"patterns.md":          false,
		"README.md":            false,
		"user_role.txt":        false, // wrong extension
		"random.md":            false,
	}
	for name, want := range cases {
		if got := IsAutoEntryFilename(name); got != want {
			t.Errorf("IsAutoEntryFilename(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuildConsolidatePrompt_EmptyAndPopulated(t *testing.T) {
	workDir := t.TempDir()

	empty, err := BuildConsolidatePrompt(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty, "(no entries yet)") {
		t.Errorf("empty workspace should render placeholder: %q", empty)
	}

	_, _ = WriteEntry(workDir, "a", Entry{
		Name: "DB rule", Description: "no mocks", Type: TypeFeedback,
		Body: "Must hit real DB.",
	})
	full, err := BuildConsolidatePrompt(workDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DB rule", "feedback", "Must hit real DB", "Return ONLY a JSON array"} {
		if !strings.Contains(full, want) {
			t.Errorf("prompt missing %q in:\n%s", want, full)
		}
	}
}
