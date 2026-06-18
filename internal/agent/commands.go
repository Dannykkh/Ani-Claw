package agent

import (
	"fmt"
	"strings"
)

// SlashCommand represents a parsed slash command from a skill.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SkillName   string `json:"skillName"`
	SkillPath   string `json:"skillPath"`
}

// ParseSlashCommands extracts slash commands from loaded skills.
func ParseSlashCommands(skills []SkillInfo) []SlashCommand {
	var commands []SlashCommand

	for _, skill := range skills {
		// The skill name itself becomes a slash command
		cmd := SlashCommand{
			Name:        skill.Name,
			Description: extractDescription(skill.Content),
			SkillName:   skill.Name,
			SkillPath:   skill.Path,
		}
		commands = append(commands, cmd)
	}

	return commands
}

// extractDescription gets the first meaningful line from skill content.
func extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines, headings starting with #
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Take first non-empty, non-heading line
		if len(line) > 100 {
			line = line[:100] + "..."
		}
		return line
	}
	return ""
}

// IsSlashCommand checks if user input starts with /
func IsSlashCommand(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), "/")
}

// ProcessSlashCommand finds the matching skill and returns the augmented prompt.
func ProcessSlashCommand(input string, skills []SkillInfo) (string, error) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return input, nil
	}

	parts := strings.SplitN(input[1:], " ", 2)
	cmdName := parts[0]
	cmdArgs := ""
	if len(parts) > 1 {
		cmdArgs = parts[1]
	}

	// Find matching skill
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, cmdName) {
			// Build augmented prompt: skill content + user args
			prompt := fmt.Sprintf("Execute this skill:\n\n--- SKILL: %s ---\n%s\n--- END SKILL ---\n",
				skill.Name, skill.Content)
			if cmdArgs != "" {
				prompt += fmt.Sprintf("\nUser arguments: %s", cmdArgs)
			}
			return prompt, nil
		}
	}

	// Built-in commands
	switch cmdName {
	case "help":
		return buildHelpText(skills), nil
	case "clear":
		return "[CLEAR_CHAT]", nil
	case "model":
		return "[SHOW_MODEL_SELECTOR]", nil
	case "plan":
		return "Enter plan mode. Before implementing anything, create a detailed plan and present it for approval. Do not write any code until the plan is approved.", nil
	case "compact":
		return "[COMPACT_CONTEXT]", nil
	}

	return "", fmt.Errorf("Unknown command: /%s. Type /help for available commands.", cmdName)
}

func buildHelpText(skills []SkillInfo) string {
	var sb strings.Builder
	sb.WriteString("Available commands:\n\n")
	sb.WriteString("  /help       — Show this help\n")
	sb.WriteString("  /plan <task> — Plan first (read-only), then Approve & Run to execute\n")
	sb.WriteString("  /undo       — Revert the agent's most recent edits\n")
	sb.WriteString("  /clear      — Clear chat\n")
	sb.WriteString("  /model      — Change model\n")
	sb.WriteString("  /compact    — Compress conversation context\n")
	sb.WriteString("\nSkill commands:\n")

	commands := ParseSlashCommands(skills)
	for _, cmd := range commands {
		desc := cmd.Description
		if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		sb.WriteString(fmt.Sprintf("  /%-20s — %s\n", cmd.Name, desc))
	}
	return sb.String()
}
