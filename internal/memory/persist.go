package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// File format — minimal frontmatter followed by markdown body.
//
//	---
//	name: <short identifier>
//	description: <one-line hook>
//	type: <user|feedback|project|reference>
//	modified: <RFC3339 timestamp>
//	---
//
//	<markdown body>
//
// The parser is intentionally strict and hand-written: we own the writer,
// so the four known keys suffice and we do not need a yaml dependency.
// Unknown keys are preserved as raw body prefix only if the frontmatter
// block is malformed — in that case we return a parse error instead of
// silently losing data.

const (
	frontmatterFence = "---"
)

// WriteEntry serializes an Entry to disk at the path implied by its
// workspace, type, and a caller-chosen slug. The file's parent directory
// is created on demand. Returns the absolute path written so the caller
// can update the Entry's File field.
//
// WriteEntry sets e.ModifiedUnix from time.Now if the field is zero, so
// callers that do not care about timestamps get sensible defaults.
func WriteEntry(workDir, slug string, e Entry) (string, error) {
	if !e.Type.Valid() {
		return "", fmt.Errorf("memory: invalid type %q", e.Type)
	}
	if e.Name == "" {
		return "", fmt.Errorf("memory: entry name is required")
	}
	if e.ModifiedUnix == 0 {
		e.ModifiedUnix = time.Now().Unix()
	}

	if err := Ensure(workDir); err != nil {
		return "", fmt.Errorf("memory: ensure dir: %w", err)
	}

	path := EntryPath(workDir, e.Type, slug)
	content := marshalEntry(e)

	// Write atomically: write to a sibling temp file, then rename. On
	// Windows rename over an existing file is atomic since Go 1.5, which
	// is the minimum we care about.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("memory: write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Clean up the temp file on rename failure so we do not leave
		// stale *.tmp files behind on a crash.
		_ = os.Remove(tmp)
		return "", fmt.Errorf("memory: rename: %w", err)
	}
	return path, nil
}

// ReadEntry loads a single memory entry from disk and returns the parsed
// Entry. The File and ModifiedUnix fields are filled from the path and
// stat — they override whatever the frontmatter claims so stale
// timestamps in the file do not mislead relevance ranking.
func ReadEntry(path string) (Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, fmt.Errorf("memory: open: %w", err)
	}
	defer f.Close()

	e, err := parseEntry(f)
	if err != nil {
		return Entry{}, fmt.Errorf("memory: parse %s: %w", path, err)
	}

	e.File = path
	if fi, err := f.Stat(); err == nil {
		e.ModifiedUnix = fi.ModTime().Unix()
	}
	return e, nil
}

// marshalEntry renders an Entry to the on-disk format. The output always
// ends in a trailing newline so concatenation in tests or tools does not
// produce awkward "abc---" seams.
func marshalEntry(e Entry) string {
	var b strings.Builder
	b.WriteString(frontmatterFence)
	b.WriteByte('\n')
	writeField(&b, "name", e.Name)
	writeField(&b, "description", e.Description)
	writeField(&b, "type", string(e.Type))
	writeField(&b, "modified", time.Unix(e.ModifiedUnix, 0).UTC().Format(time.RFC3339))
	b.WriteString(frontmatterFence)
	b.WriteString("\n\n")
	b.WriteString(strings.TrimRight(e.Body, "\n"))
	b.WriteByte('\n')
	return b.String()
}

// writeField emits a single "key: value" line. Values that contain
// newlines are folded to spaces — the frontmatter format is single-line
// per key by design, and longer content belongs in the body.
func writeField(b *strings.Builder, key, value string) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\n", " ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

// parseEntry reads a memory file from r. The frontmatter must start on
// the first line with a "---" fence and close on a later "---" fence.
// Missing frontmatter returns ErrNoFrontmatter so callers can decide
// whether to skip the file or heal it.
func parseEntry(r io.Reader) (Entry, error) {
	scanner := bufio.NewScanner(r)
	// Some memory bodies can be long; raise the default 64KB line limit
	// to 1MB. Anything larger should not live in a memory entry anyway.
	const maxLine = 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)

	// First non-empty line must be the opening fence.
	if !scanner.Scan() {
		return Entry{}, ErrNoFrontmatter
	}
	if strings.TrimSpace(scanner.Text()) != frontmatterFence {
		return Entry{}, ErrNoFrontmatter
	}

	var e Entry
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == frontmatterFence {
			// End of frontmatter — the remaining lines are the body.
			var body strings.Builder
			for scanner.Scan() {
				body.WriteString(scanner.Text())
				body.WriteByte('\n')
			}
			e.Body = strings.Trim(body.String(), "\n")
			return e, scanner.Err()
		}
		if err := applyField(&e, line); err != nil {
			return Entry{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Entry{}, err
	}
	// Reached EOF before the closing fence — treat as malformed rather
	// than silently accept a file with no body delimiter.
	return Entry{}, fmt.Errorf("unterminated frontmatter")
}

// applyField parses a single frontmatter line and updates the target
// Entry. Unknown keys are ignored (forward compatibility) rather than
// treated as errors, so adding new optional fields in the future does
// not break older readers.
func applyField(e *Entry, line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return fmt.Errorf("invalid frontmatter line %q", line)
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	switch key {
	case "name":
		e.Name = value
	case "description":
		e.Description = value
	case "type":
		e.Type = Type(value)
	case "modified":
		if value == "" {
			return nil
		}
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			e.ModifiedUnix = t.Unix()
		}
		// Silently tolerate unparseable timestamps — stat-based mtime
		// will overwrite this in ReadEntry anyway.
	}
	return nil
}

// ErrNoFrontmatter is returned by parseEntry when the input does not
// start with a "---" fence. Exported so scanners can distinguish
// "this is not a memory file" from "this is a broken memory file".
var ErrNoFrontmatter = fmt.Errorf("memory: no frontmatter")
