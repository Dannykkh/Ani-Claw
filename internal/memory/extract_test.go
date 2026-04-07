package memory

import (
	"strings"
	"testing"
)

func TestBuildExtractPrompt_IncludesManifestAndConversation(t *testing.T) {
	workDir := t.TempDir()
	// Pre-seed one scaffold and one real entry so the manifest has
	// content the prompt must reference.
	if _, err := WriteEntry(workDir, "role", Entry{
		Name: "User role", Description: "Senior Go engineer",
		Type: TypeUser, Body: "Works on proxy-go.",
	}); err != nil {
		t.Fatal(err)
	}

	convo := "user: I prefer integration tests over mocks.\nassistant: Noted."
	prompt := BuildExtractPrompt(workDir, convo)

	for _, want := range []string{
		"memory extraction assistant",
		"user_role.md",
		"I prefer integration tests over mocks",
		"Recent conversation",
		"Return ONLY a valid JSON array",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildExtractPrompt_EmptyConversation(t *testing.T) {
	prompt := BuildExtractPrompt(t.TempDir(), "")
	if !strings.Contains(prompt, "(empty)") {
		t.Errorf("empty conversation should be rendered as (empty), got:\n%s", prompt)
	}
}

func TestParseExtractedEntries_Plain(t *testing.T) {
	raw := `[
	  {
	    "name": "DB rule",
	    "description": "No mocks in integration tests",
	    "type": "feedback",
	    "body": "Must hit a real database.\n\n**Why:** migration incident."
	  },
	  {
	    "name": "User role",
	    "description": "Senior Go engineer",
	    "type": "user",
	    "body": "Works on proxy-go."
	  }
	]`
	entries, err := ParseExtractedEntries(raw)
	if err != nil {
		t.Fatalf("ParseExtractedEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Type != TypeFeedback || entries[1].Type != TypeUser {
		t.Errorf("unexpected types: %v, %v", entries[0].Type, entries[1].Type)
	}
}

func TestParseExtractedEntries_CodeFenced(t *testing.T) {
	raw := "```json\n" +
		`[{"name":"A","description":"desc","type":"project","body":"b"}]` +
		"\n```"
	entries, err := ParseExtractedEntries(raw)
	if err != nil {
		t.Fatalf("ParseExtractedEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Type != TypeProject {
		t.Errorf("code-fence stripping failed: %+v", entries)
	}
}

func TestParseExtractedEntries_WithPreamble(t *testing.T) {
	raw := "Sure, here are the memories I extracted:\n\n" +
		`[{"name":"A","description":"desc","type":"reference","body":"b"}]`
	entries, err := ParseExtractedEntries(raw)
	if err != nil {
		t.Fatalf("ParseExtractedEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Type != TypeReference {
		t.Errorf("preamble stripping failed: %+v", entries)
	}
}

func TestParseExtractedEntries_SkipsInvalidRows(t *testing.T) {
	// Mix of valid + invalid: missing name, bad type, empty body.
	raw := `[
	  {"name":"","description":"missing name","type":"user","body":"b"},
	  {"name":"Bad type","description":"ok","type":"random","body":"b"},
	  {"name":"Empty body","description":"ok","type":"project","body":""},
	  {"name":"Good","description":"ok","type":"feedback","body":"valid"}
	]`
	entries, err := ParseExtractedEntries(raw)
	if err != nil {
		t.Fatalf("ParseExtractedEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 valid entry (rest skipped), got %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "Good" {
		t.Errorf("wrong entry survived: %+v", entries[0])
	}
}

func TestParseExtractedEntries_EmptyArray(t *testing.T) {
	entries, err := ParseExtractedEntries("[]")
	if err != nil {
		t.Fatalf("empty array should parse cleanly, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseExtractedEntries_NoArray(t *testing.T) {
	_, err := ParseExtractedEntries("I refuse to produce JSON.")
	if err == nil {
		t.Error("expected error when no array is present")
	}
}

func TestParseExtractedEntries_BracketInString(t *testing.T) {
	// The body contains a literal ']' which the naive "find last ]"
	// strategy would get wrong — verify the bracket-depth walker
	// stops at the correct closing bracket.
	raw := `[{"name":"N","description":"d","type":"user","body":"has ] inside"}]`
	entries, err := ParseExtractedEntries(raw)
	if err != nil {
		t.Fatalf("ParseExtractedEntries: %v", err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Body, "]") {
		t.Errorf("bracket-in-string parse failed: %+v", entries)
	}
}

func TestSaveExtracted_PersistsAndIndexes(t *testing.T) {
	workDir := t.TempDir()

	entries := []Entry{
		{Name: "DB rule", Description: "no mocks", Type: TypeFeedback, Body: "Must hit real DB."},
		{Name: "User role", Description: "senior Go engineer", Type: TypeUser, Body: "Works on proxy-go."},
	}

	paths, trunc, err := SaveExtracted(workDir, entries)
	if err != nil {
		t.Fatalf("SaveExtracted: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if trunc.LineCapped || trunc.ByteCapped {
		t.Error("test content should not trigger caps")
	}

	// Both entries should now be scan-visible.
	scanned, err := Scan(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 2 {
		t.Fatalf("expected 2 entries after save, got %d", len(scanned))
	}

	// MEMORY.md should reference both names in the managed block.
	ctx, err := ReadIndex(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx.Content, "DB rule") || !strings.Contains(ctx.Content, "User role") {
		t.Errorf("managed block missing entries:\n%s", ctx.Content)
	}
}

func TestSaveExtracted_IsIdempotent(t *testing.T) {
	workDir := t.TempDir()
	e := Entry{Name: "Same name", Description: "desc", Type: TypeProject, Body: "body v1"}

	if _, _, err := SaveExtracted(workDir, []Entry{e}); err != nil {
		t.Fatal(err)
	}
	// Re-save with updated body — same name → same slug → same file.
	e.Body = "body v2"
	if _, _, err := SaveExtracted(workDir, []Entry{e}); err != nil {
		t.Fatal(err)
	}

	scanned, err := Scan(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 1 {
		t.Fatalf("expected exactly 1 entry after idempotent re-save, got %d", len(scanned))
	}
	if !strings.Contains(scanned[0].Body, "v2") {
		t.Errorf("expected body to be updated to v2, got %q", scanned[0].Body)
	}
}
