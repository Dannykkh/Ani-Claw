package agent

import (
	"encoding/json"
	"os"
	"strings"
)

// editToolInput is the subset of Write/Edit tool inputs needed to render a diff.
type editToolInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`
}

// editFilePath returns the (relative) file path a Write/Edit targets, for display.
func editFilePath(input json.RawMessage) string {
	var in editToolInput
	json.Unmarshal(input, &in)
	return in.FilePath
}

// editFileBefore returns the "before" text for a diff: the model's old_string
// for Edit, or the file's current on-disk content for Write. For Write it must
// be called BEFORE the tool runs (it reads the soon-to-be-overwritten file).
func editFileBefore(toolName string, input json.RawMessage, workDir string) string {
	var in editToolInput
	json.Unmarshal(input, &in)
	switch toolName {
	case "Edit":
		return in.OldString
	case "Write":
		if b, err := os.ReadFile(resolvePath(in.FilePath, workDir)); err == nil {
			return string(b)
		}
	}
	return ""
}

// editFileAfter returns the "after" text: new_string for Edit, written content
// for Write.
func editFileAfter(toolName string, input json.RawMessage) string {
	var in editToolInput
	json.Unmarshal(input, &in)
	switch toolName {
	case "Edit":
		return in.NewString
	case "Write":
		return in.Content
	}
	return ""
}

const maxDiffBytes = 4000

// unifiedLineDiff returns a line-level diff of old -> new using an LCS: context
// lines are prefixed "  ", removals "- ", additions "+ ". Returns "" when the
// texts are identical. Output is capped so a huge rewrite can't flood the stream.
func unifiedLineDiff(oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	a := strings.Split(oldText, "\n")
	b := strings.Split(newText, "\n")
	m, n := len(a), len(b)

	// Guard against a pathological LCS table on very large files: fall back to a
	// crude all-removed / all-added diff.
	if m*n > 4_000_000 {
		var sb strings.Builder
		for _, l := range a {
			sb.WriteString("- " + l + "\n")
		}
		for _, l := range b {
			sb.WriteString("+ " + l + "\n")
		}
		return capDiff(sb.String())
	}

	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var sb strings.Builder
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			sb.WriteString("  " + a[i] + "\n")
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			sb.WriteString("- " + a[i] + "\n")
			i++
		default:
			sb.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	for ; i < m; i++ {
		sb.WriteString("- " + a[i] + "\n")
	}
	for ; j < n; j++ {
		sb.WriteString("+ " + b[j] + "\n")
	}
	return capDiff(sb.String())
}

func capDiff(s string) string {
	if len(s) > maxDiffBytes {
		return s[:maxDiffBytes] + "\n... (diff truncated)"
	}
	return s
}
