package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEligible(t *testing.T) {
	cases := []struct {
		name  string
		stats TaskStats
		want  bool
	}{
		{"meets both", TaskStats{ToolCalls: 5, UniqueTools: []string{"a", "b", "c"}}, true},
		{"too few calls", TaskStats{ToolCalls: 4, UniqueTools: []string{"a", "b", "c"}}, false},
		{"too few tools", TaskStats{ToolCalls: 9, UniqueTools: []string{"a", "b"}}, false},
	}
	for _, c := range cases {
		if got := c.stats.Eligible(); got != c.want {
			t.Errorf("%s: Eligible()=%v want %v", c.name, got, c.want)
		}
	}
}

func TestParseSkill_Create(t *testing.T) {
	raw := `{"create":true,"name":"drizzle-migration","description":"Generate a migration. Trigger on schema.ts edits. Not for raw SQL.","when_to_use":"user edits schema.ts or asks to add a migration","body":"## Goal\nDo the thing.\n## Steps\n1. step"}`
	sk, err := ParseSkill(raw)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if sk == nil {
		t.Fatal("expected a skill, got nil")
	}
	if sk.Name != "drizzle-migration" {
		t.Errorf("name = %q", sk.Name)
	}
	if sk.WhenToUse == "" || sk.Description == "" || sk.Body == "" {
		t.Errorf("missing fields: %+v", sk)
	}
}

func TestParseSkill_Decline(t *testing.T) {
	sk, err := ParseSkill(`{"create":false}`)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if sk != nil {
		t.Errorf("expected nil skill on decline, got %+v", sk)
	}
}

func TestParseSkill_CodeFenceAndProseTolerant(t *testing.T) {
	raw := "Sure, here it is:\n```json\n{\"create\":true,\"name\":\"deploy-flow\",\"description\":\"Deploy. Trigger on deploy requests. Not for local runs.\",\"when_to_use\":\"user asks to deploy to prod\",\"body\":\"## Goal\\nship\"}\n```"
	sk, err := ParseSkill(raw)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if sk == nil || sk.Name != "deploy-flow" {
		t.Fatalf("expected deploy-flow, got %+v", sk)
	}
}

func TestParseSkill_MissingFieldRejected(t *testing.T) {
	// create:true but empty when_to_use must fail — a skill with no trigger is
	// exactly what this feature exists to prevent.
	raw := `{"create":true,"name":"x-flow","description":"does x","when_to_use":"","body":"## Goal"}`
	if _, err := ParseSkill(raw); err == nil {
		t.Error("expected error for empty when_to_use, got nil")
	}
}

func TestParseSkill_GenericNameRejected(t *testing.T) {
	raw := `{"create":true,"name":"Helper","description":"d","when_to_use":"w","body":"b"}`
	if _, err := ParseSkill(raw); err == nil {
		t.Error("expected error for generic name, got nil")
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"Drizzle Migration": "drizzle-migration",
		"deploy_flow":       "deploy-flow",
		"  Spaced  Name  ":  "spaced-name",
		"weird@@chars!!":    "weirdchars",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestAssembleSkillMD(t *testing.T) {
	s := &Skill{
		Name:        "deploy-flow",
		Description: "Deploy: trigger on deploy requests",
		WhenToUse:   "user asks to deploy",
		Body:        "## Goal\nship it",
	}
	md := AssembleSkillMD(s)
	for _, want := range []string{
		"name: deploy-flow",
		"description: ",
		"when_to_use: ",
		"context: fork",
		"## Goal",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("assembled SKILL.md missing %q in:\n%s", want, md)
		}
	}
	// Description contains a colon, so it must be quoted to stay valid YAML.
	if !strings.Contains(md, `description: "Deploy: trigger on deploy requests"`) {
		t.Errorf("colon-bearing description not quoted:\n%s", md)
	}
}

func TestSaveAndExistingSkillNames(t *testing.T) {
	dir := t.TempDir()
	if got := ExistingSkillNames(dir); len(got) != 0 {
		t.Errorf("fresh dir should have no skills, got %v", got)
	}

	s := &Skill{Name: "deploy-flow", Description: "d", WhenToUse: "w", Body: "## Goal"}
	path, err := SaveSkill(dir, s)
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	if want := filepath.Join(dir, "deploy-flow", "SKILL.md"); path != want {
		t.Errorf("path = %q want %q", path, want)
	}

	names := ExistingSkillNames(dir)
	if len(names) != 1 || names[0] != "deploy-flow" {
		t.Errorf("ExistingSkillNames = %v want [deploy-flow]", names)
	}
}
