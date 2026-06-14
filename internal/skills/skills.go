package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Auto-skill creation: prompt + parser + save helper.
//
// Mirrors internal/memory: this package never calls an LLM. It exposes pure
// functions so the agent layer drives the actual model call:
//
//  1. TaskStats.Eligible()    — is this conversation worth a skill?
//  2. BuildSkillPrompt(...)   — what to ask the model
//  3. <call the model>        — agent layer (skill_hook.go)
//  4. ParseSkill(response)    — validate + unmarshal
//  5. SaveSkill(...)          — assemble + write SKILL.md
//
// Keeping skills LLM-free means these functions are unit-testable without a
// live model, exactly like internal/memory.

// Eligibility thresholds — a session must clear both before we even ask the
// model whether it is skill-worthy. Mirrors Claude Code's autoSkillCreation
// (5+ tool calls, 3+ distinct tools); below this, sessions are rarely a
// reusable workflow and the model call is wasted.
const (
	MinToolCalls   = 5
	MinUniqueTools = 3
)

// TaskStats summarizes a finished session for the eligibility gate and the
// prompt header.
type TaskStats struct {
	ToolCalls        int
	UniqueTools      []string
	HadErrorRecovery bool
}

// Eligible reports whether the session is complex enough to consider.
func (s TaskStats) Eligible() bool {
	return s.ToolCalls >= MinToolCalls && len(s.UniqueTools) >= MinUniqueTools
}

// Skill is the validated result the model proposes. A nil *Skill from
// ParseSkill means the model declined (the equivalent of Claude Code's
// NO_SKILL_NEEDED).
type Skill struct {
	Name        string
	Description string
	WhenToUse   string
	Body        string
}

// genericNames are too vague to ever trigger reliably; a skill called
// "helper" or "task" is a skill nobody will ever match. Rejected at parse.
var genericNames = map[string]bool{
	"helper": true, "task": true, "workflow": true, "skill": true,
	"util": true, "utils": true, "misc": true, "general": true, "stuff": true,
}

// BuildSkillPrompt returns the full prompt asking the model to author a
// reusable SKILL.md from the just-finished conversation, or decline.
//
// existing names are passed so the model can avoid creating a near-duplicate
// of a skill that already exists — the same "do not duplicate" move the memory
// extractor makes with its manifest.
func BuildSkillPrompt(existing []string, stats TaskStats, conversation string) string {
	var b strings.Builder
	b.WriteString(skillSystemInstructions)

	b.WriteString("\n\n## Session stats\n\n")
	fmt.Fprintf(&b, "- Tool calls: %d\n- Distinct tools: %s\n- Error recovery: %v\n",
		stats.ToolCalls, strings.Join(stats.UniqueTools, ", "), stats.HadErrorRecovery)

	b.WriteString("\n## Existing skills (do not duplicate)\n\n")
	if len(existing) == 0 {
		b.WriteString("(none yet)\n")
	} else {
		for _, n := range existing {
			b.WriteString("- ")
			b.WriteString(n)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Recent conversation\n\n")
	if strings.TrimSpace(conversation) == "" {
		b.WriteString("(empty)")
	} else {
		b.WriteString(conversation)
	}

	b.WriteString("\n\n## Your response\n\n")
	b.WriteString(`Return ONLY a JSON object, no prose, no code fences:

    {
      "create": <true|false>,
      "name": "<specific-kebab-case-name>",
      "description": "<what it does + the concrete conditions that should trigger it + when NOT to use it>",
      "when_to_use": "<the activating condition, phrased so a future agent can pattern-match it>",
      "body": "<the SKILL.md markdown body: ## Goal, ## Steps, ## Gotchas — no frontmatter>"
    }

Use {"create": false} if the session is not a reusable workflow.`)
	return b.String()
}

// skillSystemInstructions is the static prefix of the skill-authoring prompt.
// The description/trigger-quality section is the whole point of the feature:
// vague descriptions are the main reason auto-created skills never get matched.
const skillSystemInstructions = `You are a skill-authoring assistant. Analyze the finished conversation and
decide whether it represents a REPEATABLE workflow worth capturing as a
reusable SKILL.md for future sessions.

## Create a skill only when ALL are true

1. The task has a clear, repeatable pattern (not a one-off investigation).
2. The workflow has 3+ distinct steps that could be reused.
3. The steps have a logical ordering a future agent would benefit from.
4. It is NOT purely exploratory (debugging one specific bug is not a skill).

If these do not hold, return {"create": false}.

## Description & trigger quality — THIS IS WHAT MATTERS MOST

The "description" and "when_to_use" fields are the ONLY thing a future agent
sees when deciding whether to trigger this skill. A vague description means the
skill is never matched — the single biggest reason auto-created skills go
unused. Make them concrete:

- State the actual trigger conditions: the user phrasings, file types,
  commands, or task shapes that should activate it.
- Include explicit non-triggers ("Do not use when ...") so it does not fire on
  the wrong task.
- Lead "when_to_use" with the activating condition, not a restatement of the title.

Weak (do not imitate): "Helps with database stuff"
Strong: "Generate and apply a Drizzle migration after schema edits. Trigger
when the user edits schema.ts, says 'add a migration', or reports schema/DB
drift. Not for raw SQL queries or seed data."

## Naming

"name" must be a specific, searchable kebab-case name for the workflow
(e.g. "drizzle-migration"), never a generic word like "helper", "task", or
"workflow".

## Body

Keep it concise (under ~200 lines). Capture the repeatable procedure, not the
specific instance, plus any gotchas and error-recovery patterns discovered.
Structure: ## Goal, ## Steps (numbered), ## Gotchas.

## Self-check before responding

Re-read your own "description"/"when_to_use": could a future agent tell, from
those two lines alone, exactly when to use AND when to skip this skill? If not,
rewrite them before returning.`

// ParseSkill parses the model's JSON response. Returns (nil, nil) when the
// model declined (create:false) — a normal outcome, not an error. Tolerates
// leading prose and code-fence wrapping like the memory parser.
func ParseSkill(raw string) (*Skill, error) {
	obj := stripToJSONObject(raw)
	if obj == "" {
		return nil, fmt.Errorf("skills: no JSON object found in response")
	}
	var dto struct {
		Create      bool   `json:"create"`
		Name        string `json:"name"`
		Description string `json:"description"`
		WhenToUse   string `json:"when_to_use"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal([]byte(obj), &dto); err != nil {
		return nil, fmt.Errorf("skills: parse JSON: %w", err)
	}
	if !dto.Create {
		return nil, nil
	}

	name := normalizeName(dto.Name)
	desc := strings.TrimSpace(dto.Description)
	when := strings.TrimSpace(dto.WhenToUse)
	body := strings.TrimSpace(dto.Body)
	if name == "" || desc == "" || when == "" || body == "" {
		return nil, fmt.Errorf("skills: create:true but a required field is empty")
	}
	if genericNames[name] {
		return nil, fmt.Errorf("skills: rejected generic skill name %q", name)
	}
	return &Skill{Name: name, Description: desc, WhenToUse: when, Body: body}, nil
}

// normalizeName lower-cases and kebab-cases the proposed name, dropping any
// character that is not a-z, 0-9, or '-'. Defends the filesystem path and
// keeps names consistent.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == ' ' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// AssembleSkillMD renders the final SKILL.md: YAML frontmatter built from the
// validated fields plus the model's body. Building the frontmatter here
// (rather than trusting the model to emit valid YAML) guarantees the
// description/when_to_use the trigger logic depends on are always present.
func AssembleSkillMD(s *Skill) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(s.Name)
	b.WriteString("\ndescription: ")
	b.WriteString(yamlScalar(s.Description))
	b.WriteString("\nwhen_to_use: ")
	b.WriteString(yamlScalar(s.WhenToUse))
	b.WriteString("\ncontext: fork\n")
	b.WriteString("---\n\n")
	b.WriteString(s.Body)
	b.WriteString("\n")
	return b.String()
}

// yamlScalar keeps a one-line value on one line and quotes it when it contains
// YAML-significant characters, escaping embedded quotes.
func yamlScalar(v string) string {
	v = strings.ReplaceAll(v, "\n", " ")
	if strings.ContainsAny(v, ":#\"'") {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}

// SaveSkill writes the assembled SKILL.md to skillsDir/<name>/SKILL.md,
// creating the directory. Last-write-wins on the name, so re-authoring the
// same workflow overwrites rather than piling up duplicates (same dedupe
// philosophy as the memory store).
func SaveSkill(skillsDir string, s *Skill) (string, error) {
	dir := filepath.Join(skillsDir, s.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("skills: mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(AssembleSkillMD(s)), 0o644); err != nil {
		return "", fmt.Errorf("skills: write %q: %w", path, err)
	}
	return path, nil
}

// ExistingSkillNames lists the immediate sub-directories of skillsDir that
// contain a SKILL.md, so BuildSkillPrompt can tell the model what already
// exists. Missing dir → empty list (not an error): a fresh project simply has
// no skills yet.
func ExistingSkillNames(skillsDir string) []string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(skillsDir, e.Name(), "SKILL.md")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names
}

// stripToJSONObject isolates the first balanced {...} object from an LLM
// response, tolerating leading prose and code-fence wrapping. Mirrors the
// memory package's stripToJSONArray. Best-effort, not adversarial-safe.
func stripToJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl > 0 {
			s = s[nl+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			switch c {
			case '\\':
				esc = true
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
