package memory

import (
	"os"
	"path/filepath"
	"strings"
)

// EntrypointName is the filename of the per-workspace memory index. It
// lives at the WORKSPACE ROOT (not inside MemoryDir) so that humans
// browsing the project see it next to README.md and CLAUDE.md.
const EntrypointName = "MEMORY.md"

// MemoryDirName is the subdirectory under the workspace root where
// per-category memory files live.
const MemoryDirName = "memory"

// Layout per workspace:
//
//	<workDir>/MEMORY.md       (index, capped at 200 lines / 25KB)
//	<workDir>/memory/
//	    ├── architecture.md
//	    ├── patterns.md
//	    ├── tools.md
//	    ├── gotchas.md
//	    ├── user_*.md
//	    ├── feedback_*.md
//	    ├── project_*.md
//	    └── reference_*.md
//
// Rationale: storing memory inside the project root means it travels with
// the project across machines, can be selectively shared via gitignore, and
// is directly editable by the user. This matches how the surrounding
// claudecode and proxy-go projects already organize memory.

// MemoryDir returns the per-workspace memory subdirectory for category and
// extracted files. Always <workDir>/memory.
func MemoryDir(workDir string) string {
	return filepath.Join(workDir, MemoryDirName)
}

// Entrypoint returns the absolute path to the workspace's MEMORY.md
// index. Lives at the workspace root, not inside MemoryDir.
func Entrypoint(workDir string) string {
	return filepath.Join(workDir, EntrypointName)
}

// EntryPath returns the absolute path for an extracted entry file with
// the given type and slug. Slug is sanitized — only lowercase letters,
// digits, dash, and underscore survive; everything else collapses to a
// single dash. The type prefix matches Claude Code's convention so the
// MEMORY.md index can group entries by type.
func EntryPath(workDir string, t Type, slug string) string {
	return filepath.Join(MemoryDir(workDir), entryFilename(t, slug))
}

// Ensure creates the workspace memory subdirectory if it does not exist.
// Safe to call repeatedly. Does not create the entrypoint file — that is
// the responsibility of the index writer, which knows whether to create a
// scaffold or merge into an existing index.
func Ensure(workDir string) error {
	return os.MkdirAll(MemoryDir(workDir), 0o755)
}

// entryFilename produces "<type>_<sanitized-slug>.md".
func entryFilename(t Type, slug string) string {
	return string(t) + "_" + sanitizeSlug(slug) + ".md"
}

// sanitizeSlug folds anything that isn't [a-z0-9_] into a single dash and
// trims leading/trailing dashes. Empty result becomes "entry" so we never
// generate a dotfile or empty name.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			// Anything else (spaces, punctuation, non-ASCII) becomes a
			// single dash. Multiple in a row collapse together.
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "entry"
	}
	return out
}
