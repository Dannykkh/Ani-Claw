package agent

import (
	"fmt"
	"strings"
)

// fuzzyReplace attempts a whitespace-insensitive replacement when an exact
// match of `old` fails. It compares `old` and `content` line-by-line after
// trimming each line, so differences in indentation or trailing whitespace
// (a common error from weaker models quoting code) don't block the edit.
// This mirrors Aider's tiered fallback (exact → whitespace-normalized).
//
// Only the first matching window is replaced. Returns (result, matched).
func fuzzyReplace(content, old, newStr string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(strings.Trim(old, "\n"), "\n")
	n := len(oldLines)
	if n == 0 || old == "" {
		return "", false
	}

	oldTrim := make([]string, n)
	for i, l := range oldLines {
		oldTrim[i] = strings.TrimSpace(l)
	}

	for start := 0; start+n <= len(contentLines); start++ {
		matched := true
		for i := 0; i < n; i++ {
			if strings.TrimSpace(contentLines[start+i]) != oldTrim[i] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		newLines := strings.Split(newStr, "\n")
		result := make([]string, 0, len(contentLines)-n+len(newLines))
		result = append(result, contentLines[:start]...)
		result = append(result, newLines...)
		result = append(result, contentLines[start+n:]...)
		return strings.Join(result, "\n"), true
	}
	return "", false
}

// closestLinesHint finds up to 3 lines in `content` that contain the first
// (trimmed) line of `old`, to help the model locate the right text on retry.
// Returns "" when nothing similar is found.
func closestLinesHint(content, old string) string {
	first := strings.TrimSpace(strings.Split(old, "\n")[0])
	if first == "" {
		return ""
	}
	var hits []string
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.TrimSpace(line), first) {
			hits = append(hits, fmt.Sprintf("  line %d: %s", i+1, strings.TrimSpace(line)))
			if len(hits) >= 3 {
				break
			}
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return "\nSimilar lines in the file:\n" + strings.Join(hits, "\n")
}
