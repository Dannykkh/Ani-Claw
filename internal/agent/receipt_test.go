package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAgentReceiptToDir(t *testing.T) {
	baseDir := t.TempDir()
	workDir := filepath.Join("D:", "git", "example repo")
	now := time.Date(2026, 6, 23, 1, 2, 3, 4, time.UTC)

	path, err := writeAgentReceiptToDir(baseDir, workDir, AgentReceipt{
		Provider:     "ollama",
		Model:        "qwen3-coder",
		ProjectType:  "go",
		Iterations:   3,
		EditedFiles:  []string{"main.go"},
		Verification: ReceiptVerification{Status: "passed", Source: "auto-verify"},
	}, now)
	if err != nil {
		t.Fatalf("writeAgentReceiptToDir() error = %v", err)
	}

	if filepath.Dir(path) == baseDir {
		t.Fatalf("receipt was not namespaced by workspace: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("receipt file not written: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var got AgentReceipt
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("receipt JSON did not unmarshal: %v", err)
	}
	if got.Version != 1 || got.CreatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("metadata = version %d createdAt %q", got.Version, got.CreatedAt)
	}
	if got.WorkDir != workDir || got.Provider != "ollama" || got.Model != "qwen3-coder" {
		t.Fatalf("identity fields not preserved: %+v", got)
	}
	if got.Verification.Status != "passed" || got.Verification.Source != "auto-verify" {
		t.Fatalf("verification = %+v", got.Verification)
	}
}

func TestReceiptVerification(t *testing.T) {
	tests := []struct {
		in     string
		status string
		source string
	}{
		{"passed", "passed", "auto-verify"},
		{"failed", "failed", "auto-verify"},
		{"", "not-run", "none"},
	}

	for _, tt := range tests {
		got := receiptVerification(tt.in)
		if got.Status != tt.status || got.Source != tt.source {
			t.Fatalf("receiptVerification(%q) = %+v, want %s/%s", tt.in, got, tt.status, tt.source)
		}
	}
}
