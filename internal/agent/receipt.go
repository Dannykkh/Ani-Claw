package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aniclew/aniclew/internal/config"
)

// AgentReceipt is the machine-readable proof that an agent run completed with
// observable state, not just a prose claim.
type AgentReceipt struct {
	Version      int                 `json:"version"`
	CreatedAt    string              `json:"createdAt"`
	WorkDir      string              `json:"workDir"`
	Provider     string              `json:"provider"`
	Model        string              `json:"model"`
	ProjectType  string              `json:"projectType"`
	PlanMode     bool                `json:"planMode"`
	Iterations   int                 `json:"iterations"`
	EditedFiles  []string            `json:"editedFiles"`
	Verification ReceiptVerification `json:"verification"`
}

type ReceiptVerification struct {
	Status string `json:"status"` // passed, failed, not-run
	Source string `json:"source"` // auto-verify, none
}

func writeAgentReceipt(workDir string, receipt AgentReceipt) (string, error) {
	baseDir := filepath.Join(filepath.Dir(config.ConfigPath()), "receipts")
	return writeAgentReceiptToDir(baseDir, workDir, receipt, time.Now().UTC())
}

func writeAgentReceiptToDir(baseDir, workDir string, receipt AgentReceipt, now time.Time) (string, error) {
	receipt.Version = 1
	receipt.CreatedAt = now.Format(time.RFC3339Nano)
	receipt.WorkDir = workDir

	dir := filepath.Join(baseDir, safeReceiptDir(workDir))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", err
	}

	name := now.Format("20060102-150405.000000000") + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func receiptVerification(testResult string) ReceiptVerification {
	switch testResult {
	case "passed":
		return ReceiptVerification{Status: "passed", Source: "auto-verify"}
	case "failed":
		return ReceiptVerification{Status: "failed", Source: "auto-verify"}
	default:
		return ReceiptVerification{Status: "not-run", Source: "none"}
	}
}

var receiptDirUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeReceiptDir(workDir string) string {
	clean := strings.TrimSpace(filepath.Clean(workDir))
	clean = strings.Trim(clean, string(filepath.Separator))
	clean = receiptDirUnsafe.ReplaceAllString(clean, "_")
	clean = strings.Trim(clean, "._-")
	if clean == "" {
		return "workspace"
	}
	if len(clean) > 120 {
		return clean[:120]
	}
	return clean
}
