package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLintFile_Go(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "ok.go")
	os.WriteFile(valid, []byte("package x\n\nfunc F() int { return 1 }\n"), 0644)
	if got := lintFile(valid); got != "" {
		t.Errorf("valid go was flagged: %s", got)
	}

	broken := filepath.Join(dir, "bad.go")
	os.WriteFile(broken, []byte("package x\n\nfunc broken( {\n"), 0644)
	if got := lintFile(broken); got == "" {
		t.Error("broken go syntax was NOT detected")
	}
}

func TestLintFile_JSON(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "ok.json")
	os.WriteFile(good, []byte(`{"a":1}`), 0644)
	if got := lintFile(good); got != "" {
		t.Errorf("valid json was flagged: %s", got)
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{ not valid"), 0644)
	if got := lintFile(bad); got == "" {
		t.Error("invalid json was NOT detected")
	}
}

func TestLintFile_UnknownExtPasses(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.xyz")
	os.WriteFile(f, []byte("garbage {{{ not a real language"), 0644)
	if got := lintFile(f); got != "" {
		t.Errorf("unknown extension should never block, got: %s", got)
	}
}
