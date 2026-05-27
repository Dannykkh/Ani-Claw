package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ── Improved Bash: configurable timeout, env vars, background ──

func executeBashV2(input json.RawMessage, workDir string) (string, bool) {
	result := ExecuteBashDeep(input, workDir, nil)
	return result.Output, result.IsError
}

// ── Improved Read: auto-detect binary, image info, better formatting ──

func executeReadV2(input json.RawMessage, workDir string) (string, bool) {
	var args struct {
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	json.Unmarshal(input, &args)

	path := resolvePath(args.FilePath, workDir)

	// File info
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	// Check if binary
	if isBinaryFile(path) {
		return fmt.Sprintf("Binary file: %s (%s)", args.FilePath, formatSize(info.Size())), false
	}

	// Check if image
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".svg" || ext == ".webp" {
		return fmt.Sprintf("Image file: %s (%s, %s)", args.FilePath, ext, formatSize(info.Size())), false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", args.FilePath, err), true
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	// Default: read up to 2000 lines
	start := args.Offset
	if start < 0 { start = 0 }
	if start > totalLines { start = totalLines }

	limit := args.Limit
	if limit <= 0 { limit = 2000 }

	end := start + limit
	if end > totalLines { end = totalLines }

	var result strings.Builder
	maxLineWidth := len(fmt.Sprintf("%d", end))

	for i := start; i < end; i++ {
		fmt.Fprintf(&result, "%*d\t%s\n", maxLineWidth, i+1, lines[i])
	}

	header := fmt.Sprintf("File: %s (%d lines, %s)", args.FilePath, totalLines, formatSize(info.Size()))
	if start > 0 || end < totalLines {
		header += fmt.Sprintf(" [showing lines %d-%d]", start+1, end)
	}
	return header + "\n" + result.String(), false
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// ── Improved Glob: recursive ** support using filepath.Walk ──

func executeGlobV2(input json.RawMessage, workDir string) (string, bool) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	json.Unmarshal(input, &args)

	dir := workDir
	if args.Path != "" {
		dir = resolvePath(args.Path, workDir)
	}

	var matches []string
	pattern := args.Pattern

	// Handle ** recursive patterns
	if strings.Contains(pattern, "**") {
		// Split: "src/**/*.go" → prefix="src", suffix="*.go"
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")

		searchDir := dir
		if prefix != "" {
			searchDir = filepath.Join(dir, prefix)
		}

		filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if suffix != "" {
				matched, _ := filepath.Match(suffix, filepath.Base(path))
				if !matched {
					return nil
				}
			}
			rel, _ := filepath.Rel(dir, path)
			matches = append(matches, rel)
			if len(matches) >= 200 {
				return filepath.SkipAll
			}
			return nil
		})
	} else {
		// Simple glob
		globPath := filepath.Join(dir, pattern)
		results, _ := filepath.Glob(globPath)
		for _, r := range results {
			rel, _ := filepath.Rel(dir, r)
			matches = append(matches, rel)
		}
	}

	sort.Strings(matches)

	if len(matches) == 0 {
		return "No files found matching: " + pattern, false
	}

	result := fmt.Sprintf("Found %d files matching '%s':\n", len(matches), pattern)
	for _, m := range matches {
		result += "  " + m + "\n"
	}
	return result, false
}

// ── Improved Grep: context lines, file type filter, count mode ──

func executeGrepV2(input json.RawMessage, workDir string) (string, bool) {
	var args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		Context    int    `json:"context"`      // lines of context (-C)
		IgnoreCase bool   `json:"ignore_case"`  // -i
		FilesOnly  bool   `json:"files_only"`   // only show file names
		MaxResults int    `json:"max_results"`
	}
	json.Unmarshal(input, &args)

	dir := workDir
	if args.Path != "" {
		dir = resolvePath(args.Path, workDir)
	}

	// Try ripgrep first, fall back to grep
	cmdName := "rg"
	cmdArgs := []string{"--no-heading", "--line-number", "--color=never"}

	if args.IgnoreCase {
		cmdArgs = append(cmdArgs, "-i")
	}
	if args.Context > 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("-C%d", args.Context))
	}
	if args.FilesOnly {
		cmdArgs = append(cmdArgs, "-l")
	}
	if args.Glob != "" {
		cmdArgs = append(cmdArgs, "--glob", args.Glob)
	}
	cmdArgs = append(cmdArgs, args.Pattern, dir)

	cmd := exec.Command(cmdName, cmdArgs...)
	out, err := cmd.CombinedOutput()

	if err != nil && len(out) == 0 {
		// Fallback to grep
		grepArgs := []string{"-rn", "--color=never"}
		if args.IgnoreCase {
			grepArgs = append(grepArgs, "-i")
		}
		if args.Context > 0 {
			grepArgs = append(grepArgs, fmt.Sprintf("-C%d", args.Context))
		}
		if args.FilesOnly {
			grepArgs = append(grepArgs, "-l")
		}
		if args.Glob != "" {
			grepArgs = append(grepArgs, "--include="+args.Glob)
		}
		grepArgs = append(grepArgs, args.Pattern, dir)

		cmd = exec.Command("grep", grepArgs...)
		out, _ = cmd.CombinedOutput()
	}

	result := strings.TrimSpace(string(out))

	// Relativize paths
	result = strings.ReplaceAll(result, dir+"/", "")
	result = strings.ReplaceAll(result, dir+"\\", "")

	// Count matches
	lines := strings.Split(result, "\n")
	matchCount := len(lines)
	if result == "" {
		matchCount = 0
	}

	maxResults := args.MaxResults
	if maxResults <= 0 { maxResults = 250 }
	if matchCount > maxResults {
		lines = lines[:maxResults]
		result = strings.Join(lines, "\n") + fmt.Sprintf("\n... (%d more results)", matchCount-maxResults)
	}

	if matchCount == 0 {
		return "No matches found for: " + args.Pattern, false
	}

	// Make paths relative
	return fmt.Sprintf("Found %d matches for '%s':\n\n%s", matchCount, args.Pattern, result), false
}

// ── Improved Edit: multi-replace, regex replace ──

func executeEditV2(input json.RawMessage, workDir string) (string, bool) {
	var args struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Regex      bool   `json:"regex"`
	}
	json.Unmarshal(input, &args)

	path := resolvePath(args.FilePath, workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", args.FilePath, err), true
	}

	content := string(data)
	var newContent string
	var count int

	if args.Regex {
		re, err := regexp.Compile(args.OldString)
		if err != nil {
			return fmt.Sprintf("Invalid regex: %v", err), true
		}
		matches := re.FindAllString(content, -1)
		count = len(matches)
		if count == 0 {
			return "Error: regex pattern not found in file", true
		}
		if args.ReplaceAll {
			newContent = re.ReplaceAllString(content, args.NewString)
		} else {
			newContent = re.ReplaceAllStringFunc(content, func(match string) string {
				if count > 0 {
					count--
					if count == len(matches)-1 {
						return re.ReplaceAllString(match, args.NewString)
					}
				}
				return match
			})
			// Simpler: just replace first
			loc := re.FindStringIndex(content)
			if loc != nil {
				newContent = content[:loc[0]] + re.ReplaceAllString(content[loc[0]:loc[1]], args.NewString) + content[loc[1]:]
			}
			count = 1
		}
	} else {
		switch {
		case strings.Contains(content, args.OldString):
			if args.ReplaceAll {
				count = strings.Count(content, args.OldString)
				newContent = strings.ReplaceAll(content, args.OldString, args.NewString)
			} else {
				count = 1
				newContent = strings.Replace(content, args.OldString, args.NewString, 1)
			}
		default:
			// Exact match failed — try a whitespace-insensitive (fuzzy) match
			// so small indentation/trailing-space differences in the model's
			// quoted text don't block the edit (Aider-style input tolerance).
			if result, ok := fuzzyReplace(content, args.OldString, args.NewString); ok {
				count = 1
				newContent = result
			} else {
				return fmt.Sprintf(
					"Error: old_string not found in %s (exact and whitespace-insensitive "+
						"match both failed).%s\nRe-read the file and copy the exact text to replace.",
					args.FilePath, closestLinesHint(content, args.OldString)), true
			}
		}
	}

	// ── Lint gate ──
	// If the file was syntactically valid before the edit, verify the edit
	// didn't break it. A new syntax error rolls back the write and reports
	// the error, which flows back to the model via the tool_result and the
	// reflection loop in RunLoop, prompting a self-correcting retry.
	preLintOK := lintFile(path) == "" // path still holds the original bytes

	err = os.WriteFile(path, []byte(newContent), 0644)
	if err != nil {
		return fmt.Sprintf("Error writing %s: %v", args.FilePath, err), true
	}

	if preLintOK {
		if lintErr := lintFile(path); lintErr != "" {
			os.WriteFile(path, data, 0644) // roll back to original
			return fmt.Sprintf(
				"Your edit introduced a syntax error and was NOT applied:\n%s\n\n"+
					"The file is unchanged. Fix your edit and try again. "+
					"Do NOT repeat the same failed edit.", strings.TrimSpace(lintErr)), true
		}
	}

	if count == 1 {
		return fmt.Sprintf("Edited %s (1 replacement)", args.FilePath), false
	}
	return fmt.Sprintf("Edited %s (%d replacements)", args.FilePath, count), false
}

// ── Improved Write: diff preview, backup ──

func executeWriteV2(input json.RawMessage, workDir string) (string, bool) {
	var args struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	json.Unmarshal(input, &args)

	path := resolvePath(args.FilePath, workDir)
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)

	// Check if file exists; keep a backup for the lint-gate rollback.
	existed := false
	var backup []byte
	if d, err := os.ReadFile(path); err == nil {
		existed = true
		backup = d
	}
	// Gate only when we have a clean baseline: a brand-new file, or an
	// existing file that was already syntactically valid.
	preLintOK := !existed || lintFile(path) == ""

	err := os.WriteFile(path, []byte(args.Content), 0644)
	if err != nil {
		return fmt.Sprintf("Error writing %s: %v", args.FilePath, err), true
	}

	if preLintOK {
		if lintErr := lintFile(path); lintErr != "" {
			if existed {
				os.WriteFile(path, backup, 0644) // roll back to original
			} else {
				os.Remove(path) // remove the newly-created broken file
			}
			return fmt.Sprintf(
				"Your write introduced a syntax error and was NOT applied:\n%s\n\n"+
					"Fix the content and try again. "+
					"Do NOT repeat the same failed write.", strings.TrimSpace(lintErr)), true
		}
	}

	lines := len(strings.Split(args.Content, "\n"))
	action := "Created"
	if existed {
		action = "Updated"
	}
	return fmt.Sprintf("%s %s (%d lines, %s)", action, args.FilePath, lines, formatSize(int64(len(args.Content)))), false
}
