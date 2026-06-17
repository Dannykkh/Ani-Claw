package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// ollamaBaseURL returns the Ollama endpoint (OLLAMA_BASE_URL or the default).
func ollamaBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL")); v != "" {
		return v
	}
	return "http://localhost:11434"
}

// detectOllamaCapabilities asks Ollama (/api/show) what a model can do, e.g.
// ["completion","tools","thinking","vision"] or ["embedding"]. The agent loop
// uses it to warn up front when a model can't tool-call (it can only chat), so
// an agentic request fails loudly with an explanation instead of silently doing
// nothing — the devstral footgun, which advertises no "tools" through Ollama's
// generic API. Returns nil on any error (treated as "capabilities unknown").
func detectOllamaCapabilities(model string) []string {
	body, _ := json.Marshal(map[string]string{"model": model})
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(ollamaBaseURL()+"/api/show", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var out struct {
		Capabilities []string `json:"capabilities"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	return out.Capabilities
}

// hasCapability reports whether caps contains name (case-insensitive).
func hasCapability(caps []string, name string) bool {
	for _, c := range caps {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

// looksContextExhausted reports whether a generation result smells like the
// prompt filled the model's context window: it stopped at the token limit
// ("max_tokens", which the translator maps from OpenAI/Ollama "length") yet
// produced almost nothing and called no tool. This separates the silent
// context-exhaustion failure — the original 8K-context footgun where the model
// emits ~1 token and stops — from a legitimate long answer that simply hit the
// output cap (which has plenty of output chars).
func looksContextExhausted(stopReason string, outChars int64, toolCount int) bool {
	return stopReason == "max_tokens" && toolCount == 0 && outChars < 200
}
