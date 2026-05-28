package agent

import (
	"path/filepath"
	"testing"
)

func TestCheckPathRelativeResolvedAgainstWorkDir(t *testing.T) {
	work := t.TempDir()
	cfg := PermissionConfig{}

	// A bare relative filename must be treated as workDir-relative and allowed,
	// regardless of the server process's current working directory.
	if ok, msg := CheckPath("notes.txt", work, cfg); !ok {
		t.Errorf("relative path should be allowed within workDir, got blocked: %s", msg)
	}
	if ok, msg := CheckPath("sub/dir/file.go", work, cfg); !ok {
		t.Errorf("nested relative path should be allowed, got blocked: %s", msg)
	}

	// Absolute path inside the workspace is allowed.
	inside := filepath.Join(work, "a", "b.txt")
	if ok, msg := CheckPath(inside, work, cfg); !ok {
		t.Errorf("absolute path inside workspace should be allowed, got: %s", msg)
	}
}

func TestCheckPathBlocksOutsideWorkspace(t *testing.T) {
	work := filepath.Join(t.TempDir(), "workspace")
	cfg := PermissionConfig{}

	// Escape via "..".
	if ok, _ := CheckPath("../escape.txt", work, cfg); ok {
		t.Errorf("path escaping workDir via .. should be blocked")
	}

	// Sibling dir that shares a name prefix must NOT be considered inside
	// (guards against the old naive strings.HasPrefix bypass).
	sibling := work + "-evil" + string(filepath.Separator) + "x.txt"
	if ok, _ := CheckPath(sibling, work, cfg); ok {
		t.Errorf("prefix-sharing sibling dir should be blocked: %s", sibling)
	}
}

func TestCheckPathBlockedPaths(t *testing.T) {
	cfg := PermissionConfig{BlockedPaths: []string{".env", "id_rsa"}}
	if ok, _ := CheckPath("config/.env", t.TempDir(), cfg); ok {
		t.Errorf("blocked path .env should be denied")
	}
}

func TestPathWithin(t *testing.T) {
	base := filepath.FromSlash("/home/u/proj")
	cases := []struct {
		target string
		want   bool
	}{
		{filepath.FromSlash("/home/u/proj"), true},
		{filepath.FromSlash("/home/u/proj/a/b"), true},
		{filepath.FromSlash("/home/u/proj-evil/x"), false},
		{filepath.FromSlash("/home/u/other"), false},
	}
	for _, c := range cases {
		if got := pathWithin(c.target, base); got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", c.target, base, got, c.want)
		}
	}
}
