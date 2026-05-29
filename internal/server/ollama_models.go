package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// handleOllamaModels returns the models actually installed in the local Ollama
// instance (GET {base}/api/tags), so the UI can offer real, pullable choices
// instead of a hardcoded guess that may not exist on this machine — the
// recurring "recommended model isn't installed" friction. Embedding-only
// models (bge, *embed*) are filtered out since they cannot chat.
//
// Always returns 200 with a (possibly empty) models array; if Ollama is
// unreachable the frontend falls back to the static list.
func (s *Server) handleOllamaModels(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(ollamaBaseURL(), "/")
	if q := strings.TrimSpace(r.URL.Query().Get("base")); q != "" {
		base = strings.TrimRight(q, "/")
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/api/tags")
	if err != nil {
		writeJSON(w, map[string]any{"models": []ollamaModel{}, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				ParameterSize string `json:"parameter_size"`
				Family        string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		writeJSON(w, map[string]any{"models": []ollamaModel{}, "error": err.Error()})
		return
	}

	out := []ollamaModel{}
	for _, m := range tags.Models {
		l := strings.ToLower(m.Name)
		if strings.Contains(l, "embed") || strings.HasPrefix(l, "bge") {
			continue // embedding models can't chat
		}
		gb := float64(m.Size) / (1024 * 1024 * 1024)
		label := fmt.Sprintf("%s (%.1fGB)", m.Name, gb)
		if m.Details.ParameterSize != "" {
			label = fmt.Sprintf("%s (%s, %.1fGB)", m.Name, m.Details.ParameterSize, gb)
		}
		out = append(out, ollamaModel{ID: m.Name, DisplayName: label, SizeGB: gb})
	}
	// Smallest first — the fastest, most likely to fit the GPU, is the safest
	// default to surface at the top.
	sort.Slice(out, func(i, j int) bool { return out[i].SizeGB < out[j].SizeGB })

	writeJSON(w, map[string]any{"models": out})
}

type ollamaModel struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	SizeGB      float64 `json:"sizeGB"`
}

func ollamaBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL")); v != "" {
		return v
	}
	return "http://localhost:11434"
}
