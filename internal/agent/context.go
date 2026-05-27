package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// LoadProjectContext reads CLAUDE.md, AGENTS.md, and .claude/ config from a workspace.
// Returns additional system prompt text to inject.
func LoadProjectContext(workDir string) string {
	var parts []string

	// ── 1. CLAUDE.md (project instructions) ──
	for _, name := range []string{"CLAUDE.md", "claude.md"} {
		content := readFileIfExists(filepath.Join(workDir, name))
		if content != "" {
			parts = append(parts, "## Project Instructions (CLAUDE.md)\n"+content)
			break
		}
	}

	// ── 2. AGENTS.md ──
	for _, name := range []string{"AGENTS.md", "agents.md"} {
		content := readFileIfExists(filepath.Join(workDir, name))
		if content != "" {
			parts = append(parts, "## Agent Instructions (AGENTS.md)\n"+content)
			break
		}
	}

	// ── 3. .claude/CLAUDE.md (user-level global instructions) ──
	home, _ := os.UserHomeDir()
	if home != "" {
		content := readFileIfExists(filepath.Join(home, ".claude", "CLAUDE.md"))
		if content != "" {
			parts = append(parts, "## Global Instructions (~/.claude/CLAUDE.md)\n"+content)
		}
	}

	// ── 4. .claude/settings.json (project-level settings) ──
	settings := readFileIfExists(filepath.Join(workDir, ".claude", "settings.json"))
	if settings != "" {
		parts = append(parts, "## Project Settings\n```json\n"+settings+"\n```")
	}

	// ── 5. README.md summary (first 50 lines for project context) ──
	for _, name := range []string{"README.md", "readme.md"} {
		content := readFileIfExists(filepath.Join(workDir, name))
		if content != "" {
			lines := strings.Split(content, "\n")
			if len(lines) > 50 {
				lines = lines[:50]
			}
			parts = append(parts, "## Project README (summary)\n"+strings.Join(lines, "\n"))
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "\n\n--- PROJECT CONTEXT ---\n" + strings.Join(parts, "\n\n")
}

// LoadSkills reads skills using default "all" source.
func LoadSkills(workDir string) []SkillInfo {
	return LoadSkillsWithSource(workDir, "all", nil)
}

// LoadSkillsWithConfig for backward compatibility.
func LoadSkillsWithConfig(workDir string, extraDirs []string) []SkillInfo {
	return LoadSkillsWithSource(workDir, "all", extraDirs)
}

// LoadSkillsWithSource reads skills filtered by source.
// source: "claude", "codex", "gemini", "all", "none"
func LoadSkillsWithSource(workDir, source string, extraDirs []string) []SkillInfo {
	if source == "none" {
		return nil
	}

	var skills []SkillInfo
	seen := make(map[string]bool)

	addSkills := func(dir string) {
		for _, s := range loadSkillsFromDir(dir) {
			if !seen[s.Name] {
				seen[s.Name] = true
				skills = append(skills, s)
			}
		}
	}

	home, _ := os.UserHomeDir()

	// Project-level always loaded (regardless of source)
	addSkills(filepath.Join(workDir, ".claude", "skills"))

	switch source {
	case "claude":
		if home != "" {
			addSkills(filepath.Join(home, ".claude", "skills"))
		}
	case "codex":
		if home != "" {
			addSkills(filepath.Join(home, ".codex", "skills"))
		}
	case "gemini":
		if home != "" {
			addSkills(filepath.Join(home, ".gemini", "skills"))
		}
	case "all":
		if home != "" {
			addSkills(filepath.Join(home, ".claude", "skills"))
			addSkills(filepath.Join(home, ".codex", "skills"))
			addSkills(filepath.Join(home, ".gemini", "skills"))
		}
	}

	for _, dir := range extraDirs {
		addSkills(dir)
	}

	return skills
}

type SkillInfo struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Path    string `json:"path"`
}

func loadSkillsFromDir(dir string) []SkillInfo {
	var skills []SkillInfo

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Check for SKILL.md inside the directory
			skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
			content := readFileIfExists(skillFile)
			if content != "" {
				skills = append(skills, SkillInfo{
					Name:    entry.Name(),
					Content: content,
					Path:    skillFile,
				})
			}
		} else if strings.HasSuffix(entry.Name(), ".md") {
			// Direct .md file as skill
			content := readFileIfExists(filepath.Join(dir, entry.Name()))
			if content != "" {
				name := strings.TrimSuffix(entry.Name(), ".md")
				skills = append(skills, SkillInfo{
					Name:    name,
					Content: content,
					Path:    filepath.Join(dir, entry.Name()),
				})
			}
		}
	}

	return skills
}

// SkillDescription extracts a one-line summary from a SKILL.md body so the
// agent loop can advertise skills by name + description instead of inlining
// every skill's full content. It prefers the YAML frontmatter `description:`
// field, then falls back to the first non-empty, non-heading prose line.
// The result is collapsed to a single line and truncated to keep the system
// prompt small — critical for local models with a 32K-ish context window.
func SkillDescription(content string) string {
	const maxLen = 200

	clean := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.Trim(s, "\"'")
		s = strings.Join(strings.Fields(s), " ") // collapse whitespace/newlines
		if len(s) > maxLen {
			s = strings.TrimSpace(s[:maxLen]) + "…"
		}
		return s
	}

	lines := strings.Split(content, "\n")

	// 1. YAML frontmatter `description:` (may be empty → multi-line block scalar)
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "---" {
				break
			}
			if rest, ok := strings.CutPrefix(line, "description:"); ok {
				// A bare block-scalar indicator (|, >, |-, >+, …) is not the
				// description itself — fall through to read the indented block.
				trimmed := strings.TrimSpace(rest)
				isBlockScalar := trimmed != "" && (trimmed[0] == '|' || trimmed[0] == '>')
				if v := clean(rest); v != "" && !isBlockScalar {
					return v
				}
				// Block scalar (description: | or >): take following indented lines.
				var block []string
				for j := i + 1; j < len(lines); j++ {
					l := lines[j]
					if strings.TrimSpace(l) == "---" || (l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t")) {
						break
					}
					block = append(block, strings.TrimSpace(l))
				}
				if v := clean(strings.Join(block, " ")); v != "" {
					return v
				}
			}
		}
	}

	// 2. First non-empty, non-heading, non-frontmatter prose line.
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || t == "---" || strings.HasPrefix(t, "#") {
			continue
		}
		if v := clean(t); v != "" {
			return v
		}
	}
	return ""
}

// LoadMCPConfig reads MCP configuration from multiple sources.
// Priority: .mcp.json > .claude/settings.json > ~/.claude/settings.json
func LoadMCPConfig(workDir string) string {
	// 1. Project .mcp.json
	for _, name := range []string{".mcp.json", "mcp.json"} {
		content := readFileIfExists(filepath.Join(workDir, name))
		if content != "" {
			return content
		}
	}

	// 2. Project .claude/settings.json → extract mcpServers
	mcpFromSettings := extractMCPFromSettings(filepath.Join(workDir, ".claude", "settings.json"))
	if mcpFromSettings != "" {
		return mcpFromSettings
	}

	// 3. Global ~/.claude/settings.json
	home, _ := os.UserHomeDir()
	if home != "" {
		mcpFromSettings = extractMCPFromSettings(filepath.Join(home, ".claude", "settings.json"))
		if mcpFromSettings != "" {
			return mcpFromSettings
		}
	}

	return ""
}

// extractMCPFromSettings extracts mcpServers from a Claude settings.json file.
func extractMCPFromSettings(path string) string {
	content := readFileIfExists(path)
	if content == "" {
		return ""
	}

	var settings map[string]interface{}
	if json.Unmarshal([]byte(content), &settings) != nil {
		return ""
	}

	// Look for mcpServers key
	mcpServers, ok := settings["mcpServers"]
	if !ok {
		return ""
	}

	// Wrap in expected format
	wrapped := map[string]interface{}{
		"mcpServers": mcpServers,
	}
	data, _ := json.Marshal(wrapped)
	return string(data)
}

func readFileIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	// Limit size to prevent huge files from flooding the prompt
	if len(content) > 20000 {
		content = content[:20000] + "\n... (truncated)"
	}
	return content
}
