package agent

import (
	"strings"
	"testing"
)

func TestSkillDescription(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "frontmatter description inline",
			in:   "---\nname: foo\ndescription: Does a useful thing.\n---\n# Foo\nbody",
			want: "Does a useful thing.",
		},
		{
			name: "frontmatter quoted",
			in:   "---\ndescription: \"Quoted desc\"\n---\n",
			want: "Quoted desc",
		},
		{
			name: "frontmatter block scalar",
			in:   "---\nname: bar\ndescription: |\n  Line one\n  line two\n---\n",
			want: "Line one line two",
		},
		{
			name: "no frontmatter falls back to first prose line",
			in:   "# Heading\n\nThis is the first prose line.\nmore",
			want: "This is the first prose line.",
		},
		{
			name: "empty content",
			in:   "",
			want: "",
		},
		{
			name: "only headings",
			in:   "# A\n## B\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SkillDescription(tt.in); got != tt.want {
				t.Errorf("SkillDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSkillDescriptionTruncates(t *testing.T) {
	long := "---\ndescription: " + strings.Repeat("x", 500) + "\n---\n"
	got := SkillDescription(long)
	// 200 chars + ellipsis rune
	if len([]rune(got)) > 201 {
		t.Errorf("expected truncation to ~200 chars, got %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got[len(got)-10:])
	}
}
