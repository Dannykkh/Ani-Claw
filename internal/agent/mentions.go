package agent

import (
	"os"
	"regexp"
	"strings"
)

// fileMentionRe matches "@path" file references: a path-like token that ends in
// an extension (so "@user" or "@TODO" don't match, but "@main.py" and
// "@backend/app/main.py" do). Existence is checked separately.
var fileMentionRe = regexp.MustCompile(`@([A-Za-z0-9_][A-Za-z0-9_./\-]*\.[A-Za-z0-9]+)`)

const maxMentionBytes = 8000

// expandFileMentions scans text for "@path" references, reads the ones that
// resolve to real files under workDir, and returns a "## Referenced files"
// block to prepend to the prompt — so the model gets them up front instead of
// crawling to find them (a big win for local models that explore inefficiently).
// Returns "" and nil when nothing matches a real file.
func expandFileMentions(text, workDir string) (block string, files []string) {
	matches := fileMentionRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", nil
	}
	seen := map[string]bool{}
	var sb strings.Builder
	for _, m := range matches {
		rel := m[1]
		if seen[rel] {
			continue
		}
		abs := resolvePath(rel, workDir)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue // not a real file — leave the @token alone
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		seen[rel] = true
		files = append(files, rel)
		content := string(b)
		if len(content) > maxMentionBytes {
			content = content[:maxMentionBytes] + "\n… (truncated)"
		}
		sb.WriteString("### @" + rel + "\n```\n" + content + "\n```\n\n")
	}
	if len(files) == 0 {
		return "", nil
	}
	return "## Referenced files (full contents are below — do NOT call Read or LS on these; use them directly)\n" + sb.String(), files
}
