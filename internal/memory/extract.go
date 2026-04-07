package memory

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Extraction prompt + parser + save helper.
//
// The memory package intentionally does NOT call an LLM itself — that
// would force a dependency on internal/providers and create a circular
// coupling with the agent layer. Instead we expose three pure functions
// so the agent can:
//
//  1. BuildExtractPrompt(workDir, conversation)  // what to ask the model
//  2. <call the model via providers>              // agent layer
//  3. ParseExtractedEntries(response)              // validate + unmarshal
//  4. SaveExtracted(workDir, entries)              // persist + update index
//
// Keeping memory at the bottom of the dependency graph means tests here
// never need a live model and the storage layer can be reused from other
// contexts (CLI import, backfill scripts) without dragging providers in.

// BuildExtractPrompt returns the full prompt to send to the model when
// asking it to extract durable memories from a conversation.
//
// The prompt names the existing memory files so the model does not
// duplicate what is already captured, explains the four types with
// their intent and "do not save" rules, and forces the response format
// to a bare JSON array so ParseExtractedEntries can consume it with a
// single unmarshal.
//
// conversation should already be rendered to a single text block by the
// caller (typically by joining user+assistant messages with role labels).
// Passing an empty string is legal; the prompt will still be valid and
// the model should return [].
func BuildExtractPrompt(workDir, conversation string) string {
	raws, _ := ScanRaw(workDir)
	manifest := FormatManifest(raws)

	var b strings.Builder
	b.WriteString(extractSystemInstructions)
	b.WriteString("\n\n## Existing memory files (do not duplicate)\n\n")
	b.WriteString(manifest)
	b.WriteString("\n\n## Recent conversation\n\n")
	if strings.TrimSpace(conversation) == "" {
		b.WriteString("(empty)")
	} else {
		b.WriteString(conversation)
	}
	b.WriteString("\n\n## Your response\n\n")
	b.WriteString("Return ONLY a valid JSON array. Use [] if nothing is worth saving.\n")
	return b.String()
}

// extractSystemInstructions is the static prefix of the extraction
// prompt. Kept as a package constant so golden tests can assert against
// the exact wording when it matters. Derived from Claude Code's
// extractMemories/prompts.ts guidance but tightened to be type-strict
// and JSON-only (Claude Code's version accepts free-form markdown with
// a harness on top — we do not have that harness yet in Go).
const extractSystemInstructions = `You are a memory extraction assistant. Analyze the recent conversation and
return a JSON array of durable memories worth saving for future sessions.

## Output format

Return ONLY a JSON array, no prose, no code fences. Each element is:

    {
      "name": "<short stable identifier>",
      "description": "<one-line hook, under 150 chars>",
      "type": "<user|feedback|project|reference>",
      "body": "<full markdown body>"
    }

Empty array [] if nothing in the conversation is worth saving.

## Memory types

- user: the user's role, preferences, responsibilities, or knowledge.
  Use when you learn WHO they are or HOW they want to work. Tailor future
  answers to this profile.

- feedback: corrections OR validations from the user.
  Save from failure AND success: if you only save corrections you will
  drift from approaches the user has already confirmed. Include **Why:**
  (the reason the user gave) and **How to apply:** (when this kicks in)
  lines so edge cases can be judged, not blindly followed.

- project: ongoing work, goals, decisions, or incidents not derivable
  from code or git history. Convert relative dates ("Thursday") to
  absolute ones ("2026-04-09"). Include **Why:** and **How to apply:**
  lines. Project memories decay fast — the why helps judge staleness.

- reference: pointers to external systems and their purpose (Linear
  projects, Grafana dashboards, Slack channels).

## Do NOT save

- Anything derivable by reading the current project state.
- Git history, recent changes, or authorship (git log is authoritative).
- Debugging solutions (the fix is in the code; the commit has the why).
- Content already present in the existing memory files listed below.
- Ephemeral task details from the current conversation.

## Guidance

- Prefer 0-3 entries per conversation. Quality over quantity.
- Names should be stable enough to dedupe against future entries.
- Descriptions must fit in a MEMORY.md index row (under ~150 chars).
- Bodies should be short: 3-10 lines of markdown is typical.`

// ExtractedEntry is the DTO we accept from the model's JSON response.
// Kept separate from Entry so validation errors point at JSON field
// names and so future schema drift (extra fields, renamed fields) can
// be absorbed here without touching Entry.
type ExtractedEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Body        string `json:"body"`
}

// ParseExtractedEntries accepts the raw model response and returns a
// slice of Entry ready to WriteEntry. It tolerates two common LLM
// cosmetic issues that would otherwise break encoding/json: leading
// prose before the array, and code-fence wrapping ("```json ... ```").
// Everything else is treated as malformed.
//
// Validation rules:
//   - name is required
//   - description is required
//   - type must be one of the four canonical types
//   - body is required (empty body is a bug; the model was asked to
//     produce markdown content)
//
// Invalid entries are SKIPPED rather than failing the whole batch, so a
// single bad row does not throw away the rest of the extraction. The
// returned error is non-nil only when the top-level JSON parse fails.
func ParseExtractedEntries(raw string) ([]Entry, error) {
	trimmed := stripToJSONArray(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("memory: no JSON array found in response")
	}

	var dtos []ExtractedEntry
	if err := json.Unmarshal([]byte(trimmed), &dtos); err != nil {
		return nil, fmt.Errorf("memory: parse extracted JSON: %w", err)
	}

	out := make([]Entry, 0, len(dtos))
	for _, d := range dtos {
		e, ok := validateExtracted(d)
		if !ok {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// stripToJSONArray trims common cosmetic framing from an LLM response
// to isolate the JSON array. It:
//   - removes a leading fenced code block if present ("```json" / "```"),
//   - returns the substring from the first '[' to the matching ']'.
//
// If no bracketed array is found, returns "". This is best-effort and
// not a full JSON parser — the intent is to survive the typical ways
// models wrap their output, not to sanitize adversarial input.
func stripToJSONArray(s string) string {
	s = strings.TrimSpace(s)

	// Strip code fence wrapping.
	if strings.HasPrefix(s, "```") {
		// Drop the opening fence line.
		if nl := strings.IndexByte(s, '\n'); nl > 0 {
			s = s[nl+1:]
		}
		// Drop the closing fence.
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}

	start := strings.IndexByte(s, '[')
	if start < 0 {
		return ""
	}
	// Walk forward tracking bracket depth so we stop at the matching ']'
	// even if the body contains strings with brackets. This is a tiny
	// state machine that respects string escapes.
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
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// validateExtracted applies the required-field and type rules and
// returns the resulting Entry. Returns ok=false if any rule fails.
func validateExtracted(d ExtractedEntry) (Entry, bool) {
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	d.Body = strings.TrimSpace(d.Body)
	t := Type(strings.TrimSpace(strings.ToLower(d.Type)))

	if d.Name == "" || d.Description == "" || d.Body == "" {
		return Entry{}, false
	}
	if !t.Valid() {
		return Entry{}, false
	}
	return Entry{
		Name:        d.Name,
		Description: d.Description,
		Type:        t,
		Body:        d.Body,
	}, true
}

// SaveExtracted persists a batch of validated entries and then refreshes
// MEMORY.md so the new entries appear in the managed block on next read.
// The slug used for each file is derived from the entry name so it is
// stable across re-extractions of the same fact — this is how dedupe
// against already-saved memories ends up being an atomic file overwrite
// rather than a growing pile of near-duplicates.
//
// Returns the paths written (in input order) and the truncation info
// from the final UpdateIndex so callers can warn when MEMORY.md is
// pushing against the caps.
func SaveExtracted(workDir string, entries []Entry) ([]string, EntrypointTruncation, error) {
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		p, err := WriteEntry(workDir, e.Name, e)
		if err != nil {
			return paths, EntrypointTruncation{}, fmt.Errorf("memory: save %q: %w", e.Name, err)
		}
		paths = append(paths, p)
	}

	// Re-scan AFTER writing so UpdateIndex sees the new entries plus any
	// pre-existing ones. We do not trust the caller to pass the full set.
	all, err := Scan(workDir)
	if err != nil {
		return paths, EntrypointTruncation{}, fmt.Errorf("memory: rescan: %w", err)
	}
	trunc, err := UpdateIndex(workDir, all)
	if err != nil {
		return paths, trunc, fmt.Errorf("memory: update index: %w", err)
	}
	return paths, trunc, nil
}
