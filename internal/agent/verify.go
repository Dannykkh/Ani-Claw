package agent

import (
	"encoding/json"
	"os"
	"strings"
)

// maxVerifyAttempts bounds how many times auto-verify asks the model to fix
// failing tests before giving up, so an unrelated or pre-existing failure can
// never loop forever.
const maxVerifyAttempts = 2

// autoVerifyEnabled reports whether post-edit auto-verification is active. On by
// default; set ANICLEW_AUTOVERIFY to off/0/false to disable (e.g. for very slow
// suites).
func autoVerifyEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ANICLEW_AUTOVERIFY")))
	return v != "off" && v != "0" && v != "false"
}

// runAutoVerify runs the project's test suite (auto-detected) after the agent
// has edited files — the Claude-Code-style "edit → test → fix" loop. Returns the
// output, whether it failed, and whether a runner actually ran. ran is false
// when the project type is unknown or has no tests, so the caller skips silently
// instead of nagging about a missing suite.
func runAutoVerify(workDir string) (output string, failed bool, ran bool) {
	switch DetectProject(workDir).Type {
	case "go", "node", "python", "rust":
		// has a known runner
	default:
		return "", false, false
	}
	out, isErr := executeTest(json.RawMessage(`{}`), workDir)
	// executeTest returns these sentinels when there is nothing to run.
	if strings.Contains(out, "No test runner") || strings.Contains(out, "No test output") {
		return "", false, false
	}
	return out, isErr, true
}
