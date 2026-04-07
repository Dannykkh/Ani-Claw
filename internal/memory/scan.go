package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scan walks the workspace's memory directory and returns every parseable
// Entry it finds. Files that exist but lack frontmatter (e.g., the
// hand-edited architecture.md / patterns.md scaffolds from CLAUDE.md
// conventions) are NOT returned as Entries — they are listed separately
// via ScanRaw below, because consolidation treats them differently.
//
// Missing directory is not an error; Scan returns an empty slice so that
// calling Scan on a fresh workspace does not force callers to pre-check.
func Scan(workDir string) ([]Entry, error) {
	dir := MemoryDir(workDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Entry
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		full := filepath.Join(dir, de.Name())
		e, err := ReadEntry(full)
		if err != nil {
			// Scaffold files without frontmatter are expected; everything
			// else is logged by the caller. We skip the former and
			// propagate the latter via a zero-Name entry so the caller
			// can count failures without aborting the scan.
			if errors.Is(err, ErrNoFrontmatter) {
				continue
			}
			continue
		}
		out = append(out, e)
	}
	// Sort by type, then name, so downstream rendering is deterministic.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// RawFile represents a markdown file in the memory directory that is
// NOT a structured Entry — typically a hand-edited scaffold like
// architecture.md or patterns.md. These files still matter for
// consolidation prompts (they tell the agent what categories already
// exist) but they are not individually promoted to the MEMORY.md index.
type RawFile struct {
	Name string // base filename, e.g. "architecture.md"
	Path string // absolute path
	Size int64  // file size in bytes
}

// ScanRaw returns all markdown files in the memory directory — both the
// Entry-carrying files and the scaffold files — as a flat metadata
// listing suitable for formatMemoryManifest-style prompts.
func ScanRaw(workDir string) ([]RawFile, error) {
	dir := MemoryDir(workDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []RawFile
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, RawFile{
			Name: de.Name(),
			Path: filepath.Join(dir, de.Name()),
			Size: info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// FormatManifest renders the raw-file listing the way an extraction or
// consolidation prompt needs it: one line per file with a size hint so
// the agent can judge which files are worth fully re-reading vs. just
// referencing. Inspired by Claude Code's formatMemoryManifest in
// memdir/memoryScan.ts, kept small and deterministic.
func FormatManifest(raws []RawFile) string {
	if len(raws) == 0 {
		return "(no memory files yet)"
	}
	var b strings.Builder
	for _, r := range raws {
		b.WriteString("- ")
		b.WriteString(r.Name)
		b.WriteString(" (")
		b.WriteString(formatBytes(int(r.Size)))
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
