package agent

import (
	"strings"
	"testing"
)

func TestFuzzyReplace_WhitespaceInsensitive(t *testing.T) {
	// File uses 8-space indentation.
	content := "package x\n\nfunc F() {\n        return 1\n}\n"
	// Model quoted it with 4-space indentation — exact match would fail.
	old := "func F() {\n    return 1\n}"
	newStr := "func F() {\n\treturn 2\n}"

	result, ok := fuzzyReplace(content, old, newStr)
	if !ok {
		t.Fatal("fuzzy match failed despite only-whitespace difference")
	}
	if !strings.Contains(result, "return 2") {
		t.Errorf("replacement not applied: %q", result)
	}
	if strings.Contains(result, "return 1") {
		t.Errorf("original text still present: %q", result)
	}
}

func TestFuzzyReplace_NoMatch(t *testing.T) {
	content := "line a\nline b\n"
	if _, ok := fuzzyReplace(content, "totally different\ntext here", "x"); ok {
		t.Error("should not match unrelated text")
	}
}

func TestFuzzyReplace_EmptyOld(t *testing.T) {
	if _, ok := fuzzyReplace("some content", "", "x"); ok {
		t.Error("empty old_string must not match")
	}
}

func TestClosestLinesHint(t *testing.T) {
	content := "func Alpha() {}\nfunc Beta() {}\nfunc Alpha2() {}\n"
	hint := closestLinesHint(content, "func Alpha() {")
	if !strings.Contains(hint, "line 1") {
		t.Errorf("expected a hint pointing at line 1, got: %q", hint)
	}
}
