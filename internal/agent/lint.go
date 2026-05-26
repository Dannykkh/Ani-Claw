package agent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// lintFile runs a fast syntax check on a file based on its extension.
//
// It returns "" when the file is syntactically OK — and also when no linter
// is available for the language or the linter binary is not installed. We
// never block on unknown languages or missing tools; this mirrors the
// SWE-agent ACI philosophy ("only block on a syntax error we can prove").
//
// When a syntax error is found, it returns the linter's error text so the
// caller can surface it to the model for self-correction (reflection).
func lintFile(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	// JSON: validate in-process, no external tool required.
	if ext == ".json" {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		if !json.Valid(data) {
			return "invalid JSON syntax"
		}
		return ""
	}

	var cmd *exec.Cmd
	switch ext {
	case ".go":
		// gofmt -e exits non-zero and prints the parse error on bad syntax.
		cmd = exec.Command("gofmt", "-e", path)
	case ".py":
		// py_compile reports only syntax errors (no style noise).
		cmd = exec.Command("python3", "-m", "py_compile", path)
	case ".js", ".mjs", ".cjs", ".jsx":
		cmd = exec.Command("node", "--check", path)
	default:
		return "" // no linter for this language — don't block the edit
	}

	out, err := cmd.CombinedOutput()
	if err == nil {
		return "" // exit 0 → syntactically valid
	}
	// If the linter binary itself is missing, skip the gate (best-effort).
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return ""
	}
	// Non-zero exit → syntax error. Return the tool's message.
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return msg
}
