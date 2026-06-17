package agent

import "strings"

// ModelProfile is per-model agent-loop tuning, applied by name match for local
// (Ollama/SGLang) models. It sits between explicit user config and the generic
// local default in the resolution chain:
//
//	tool budget : env ANICLEW_MAX_TOOLS > config localToolBudget > profile > default(16)
//	temperature : config agentTemperature > profile (default 0)
type ModelProfile struct {
	name        string  // label, shown to the user and logged
	toolBudget  int     // max tools handed to the model
	temperature float64 // sampling temperature
}

// modelProfiles are matched in order; the first whose pattern is a
// case-insensitive substring of the model id wins. Put specific patterns (e.g.
// "coder") before generic size buckets.
//
// Provenance:
//   - "coder" 16/0 is VERIFIED (qwen3-coder:30b: reliable multi-file edits).
//   - the rest are conservative HEURISTICS: smaller models tolerate fewer tools
//     (see translate.PruneTools — tool-count hallucination), and temperature
//     stays 0 everywhere because low temp is what made local tool calling
//     reliable (prose-drift at the provider default). Tune via config as needed.
var modelProfiles = []struct {
	match   string
	profile ModelProfile
}{
	{"coder", ModelProfile{"coder (coding-tuned)", 16, 0}},
	{"devstral", ModelProfile{"devstral (agentic-coding)", 16, 0}},
	{"deepseek-r1", ModelProfile{"deepseek-r1 (reasoning)", 14, 0}},
	{"qwq", ModelProfile{"qwq (reasoning)", 14, 0}},
	// size buckets (Ollama tag form, e.g. "qwen3:8b"): smaller -> fewer tools.
	{":3b", ModelProfile{"small (<=4B)", 8, 0}},
	{":4b", ModelProfile{"small (<=4B)", 8, 0}},
	{":7b", ModelProfile{"small (7-8B)", 10, 0}},
	{":8b", ModelProfile{"small (7-8B)", 10, 0}},
}

// profileFor returns the matching profile and true, or the generic local
// default and false when nothing matches.
func profileFor(model string) (ModelProfile, bool) {
	m := strings.ToLower(model)
	for _, p := range modelProfiles {
		if strings.Contains(m, p.match) {
			return p.profile, true
		}
	}
	return ModelProfile{"local-default", defaultLocalToolBudget, defaultLocalTemperature}, false
}
