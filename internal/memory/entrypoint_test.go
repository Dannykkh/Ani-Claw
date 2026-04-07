package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// osStat is a tiny indirection so the new Ensure test can stat without
// importing os into the existing test cases above. Keeps the diff focused.
var osStat = os.Stat

func TestTruncateEntrypoint_NoCap(t *testing.T) {
	in := "# Hi\n\nshort content\n"
	got := TruncateEntrypoint(in)

	if got.LineCapped || got.ByteCapped {
		t.Fatalf("expected no truncation, got line=%v byte=%v", got.LineCapped, got.ByteCapped)
	}
	if !strings.Contains(got.Content, "short content") {
		t.Fatalf("expected body preserved, got %q", got.Content)
	}
	if strings.Contains(got.Content, "WARNING") {
		t.Fatal("expected no warning when within caps")
	}
}

func TestTruncateEntrypoint_LineCap(t *testing.T) {
	// 250 lines — clearly above the 200 line cap, well below the byte cap.
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = "line"
	}
	in := strings.Join(lines, "\n")

	got := TruncateEntrypoint(in)
	if !got.LineCapped {
		t.Fatal("expected LineCapped=true")
	}
	if got.ByteCapped {
		t.Fatal("expected ByteCapped=false (raw is small)")
	}
	if got.LineCount != 250 {
		t.Fatalf("expected original LineCount=250, got %d", got.LineCount)
	}
	if !strings.Contains(got.Content, "WARNING") {
		t.Fatal("expected warning when truncated")
	}
	if !strings.Contains(got.Content, "250 lines") {
		t.Fatalf("expected warning to mention 250 lines, got %q", got.Content)
	}

	// Body part (before warning) should have at most MaxEntrypointLines.
	body := strings.SplitN(got.Content, "\n\n> WARNING:", 2)[0]
	bodyLines := strings.Count(body, "\n") + 1
	if bodyLines > MaxEntrypointLines {
		t.Fatalf("body has %d lines, expected ≤ %d", bodyLines, MaxEntrypointLines)
	}
}

func TestTruncateEntrypoint_ByteCapSingleHugeLine(t *testing.T) {
	// One single line bigger than MaxEntrypointBytes — exercises the
	// "no newline before cut" fallback in TruncateEntrypoint.
	in := strings.Repeat("x", MaxEntrypointBytes+500)
	got := TruncateEntrypoint(in)

	if !got.ByteCapped {
		t.Fatal("expected ByteCapped=true")
	}
	if got.LineCapped {
		t.Fatal("expected LineCapped=false (1 line)")
	}
	body := strings.SplitN(got.Content, "\n\n> WARNING:", 2)[0]
	if len(body) > MaxEntrypointBytes {
		t.Fatalf("body length %d exceeds MaxEntrypointBytes=%d", len(body), MaxEntrypointBytes)
	}
	if !strings.Contains(got.Content, "index entries are too long") {
		t.Fatalf("expected byte-only warning phrasing, got %q", got.Content)
	}
}

func TestTruncateEntrypoint_BothCaps(t *testing.T) {
	// 250 lines AND each line is large enough to also bust the byte cap.
	lines := make([]string, 250)
	bigChunk := strings.Repeat("y", 200)
	for i := range lines {
		lines[i] = bigChunk
	}
	in := strings.Join(lines, "\n")

	got := TruncateEntrypoint(in)
	if !got.LineCapped || !got.ByteCapped {
		t.Fatalf("expected both caps fired, got line=%v byte=%v", got.LineCapped, got.ByteCapped)
	}
	if !strings.Contains(got.Content, "lines and") {
		t.Fatalf("expected combined warning phrasing, got %q", got.Content)
	}
}

func TestPathsLiveInsideWorkspace(t *testing.T) {
	// Per project layout: <workDir>/MEMORY.md + <workDir>/memory/...
	// The directory must be a child of the workspace, not a global hash dir.
	// Use t.TempDir to get an OS-native path so slash comparisons work on
	// both Windows and Unix.
	workDir := t.TempDir()

	dir := MemoryDir(workDir)
	if !strings.HasPrefix(dir, workDir) {
		t.Fatalf("MemoryDir should be inside workDir, got %s", dir)
	}
	if filepath.Base(dir) != MemoryDirName {
		t.Fatalf("MemoryDir should end in %s, got %s", MemoryDirName, dir)
	}

	ep := Entrypoint(workDir)
	if !strings.HasPrefix(ep, workDir) {
		t.Fatalf("Entrypoint should be inside workDir, got %s", ep)
	}
	if filepath.Base(ep) != EntrypointName {
		t.Fatalf("Entrypoint should end in %s, got %s", EntrypointName, ep)
	}
	// MEMORY.md must live AT the workspace root, not inside the memory dir.
	if filepath.Dir(ep) != filepath.Clean(workDir) {
		t.Fatalf("Entrypoint must live at workspace root, got parent %s", filepath.Dir(ep))
	}

	p := EntryPath(workDir, TypeFeedback, "Tests Must Hit Real DB")
	if filepath.Base(p) != "feedback_tests-must-hit-real-db.md" {
		t.Fatalf("unexpected EntryPath basename: %s", filepath.Base(p))
	}
	if filepath.Dir(p) != dir {
		t.Fatalf("EntryPath should live directly inside MemoryDir (%s), got parent %s", dir, filepath.Dir(p))
	}
}

func TestEnsureCreatesDir(t *testing.T) {
	workDir := t.TempDir()
	if err := Ensure(workDir); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	// Calling twice should still succeed.
	if err := Ensure(workDir); err != nil {
		t.Fatalf("Ensure (second call) failed: %v", err)
	}
	// Directory should exist now.
	if _, err := osStat(MemoryDir(workDir)); err != nil {
		t.Fatalf("MemoryDir should exist after Ensure: %v", err)
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := map[string]string{
		"Hello World":                "hello-world",
		"  spaces  ":                 "spaces",
		"weird!!chars??":             "weird-chars",
		"":                           "entry",
		"---":                        "entry",
		"keep_under_score":           "keep_under_score",
		"한글-mixed":                   "mixed", // non-ascii drops to dash, then trimmed
		"multiple    spaces between": "multiple-spaces-between",
	}
	for in, want := range cases {
		got := sanitizeSlug(in)
		if got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTypeValid(t *testing.T) {
	for _, ok := range []Type{TypeUser, TypeFeedback, TypeProject, TypeReference} {
		if !ok.Valid() {
			t.Errorf("expected %q to be Valid", ok)
		}
	}
	for _, bad := range []Type{"", "random", "USER"} {
		if Type(bad).Valid() {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}
