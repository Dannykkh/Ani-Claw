package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aniclew/aniclew/internal/hooks"
	"github.com/aniclew/aniclew/internal/types"
)

// heartbeat emits an elapsed-time + output-size signal once per second while
// the agent waits on (and consumes) the provider stream. A slow local model
// (qwen3 on a 16GB GPU offloads to CPU; prompt prefill before the first token
// can be many seconds of pure silence) otherwise looks hung — the agent loop
// emits nothing of its own between the pre-call status and the provider's first
// delta. This is the source of truth every client renders, modeled on Claude
// Code's "Thinking… (Ns · ↑N tokens)" status line.
//
// stop() must be called before the eventCh is closed (it joins the goroutine),
// so callers defer/scope it to a single provider call.
func startHeartbeat(eventCh chan<- Event, outChars *int64) (stop func()) {
	start := time.Now()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				eventCh <- Event{Type: "heartbeat", Data: map[string]interface{}{
					"elapsedMs": time.Since(start).Milliseconds(),
					"chars":     atomic.LoadInt64(outChars),
				}}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

const baseSystemPrompt = `You are AniClew, an expert coding agent. You act by CALLING TOOLS, not by describing actions.

## Acting vs. talking (read this first)
The ONLY way to change the filesystem, run code, or inspect the project is to emit a tool call. Writing code or commands in your reply text does NOTHING — no file is created, nothing runs.
- When the user asks you to create/modify/run/check something, your response MUST contain a tool call. Do not answer with prose or a code block alone.
- NEVER claim you "created", "wrote", "updated", or "ran" anything unless a tool call actually did it in this turn. Saying so without a tool call is a hallucination and is wrong.
- Prefer acting over explaining. Make the tool call first; keep any prose short. After tools report success, give a brief confirmation of what the tools actually did.
- If a task needs several steps, call tools across multiple turns until it is genuinely done.

## Tools: Bash, Read, Write, Edit, Glob, Grep, Git, LS, WebSearch, WebFetch, TaskCreate/Update/List, NotebookRead/Edit, Screenshot, MouseClick, TypeText, OpenApp, FileManager, Clipboard

## Rules
- To create a new file, call Write. To change an existing file, Read it first, then call Edit.
- Use Glob/Grep to find files instead of guessing paths
- Run tests after changes when possible
- For git: use Git tool (not Bash)
- Keep changes minimal and focused
- Be concise`

var langInstructions = map[string]string{
	"ko":   "\n\nIMPORTANT: Always respond in Korean (한국어). Code and file paths stay in English, but all explanations, comments to the user, and descriptions must be in Korean.",
	"en":   "\n\nIMPORTANT: Always respond in English.",
	"ja":   "\n\nIMPORTANT: Always respond in Japanese (日本語). Code and file paths stay in English, but all explanations must be in Japanese.",
	"zh":   "\n\nIMPORTANT: Always respond in Chinese (中文). Code and file paths stay in English, but all explanations must be in Chinese.",
	"auto": "", // no language instruction — let the model follow the user's language
}

func buildSystemPrompt(responseLang string) string {
	instruction := langInstructions[responseLang]
	if instruction == "" {
		instruction = langInstructions["auto"]
	}
	return baseSystemPrompt + instruction
}

// Event is sent to the client via SSE during the agent loop.
type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// RunLoop executes the agent loop: prompt → LLM → tool_use → execute → repeat.
func RunLoop(
	ctx context.Context,
	provider types.Provider,
	model string,
	userMessages []types.Message,
	workDir string,
	responseLang string,
	eventCh chan<- Event,
) {
	defer close(eventCh)

	messages := make([]types.Message, len(userMessages))
	copy(messages, userMessages)
	tools := AllToolDefs(workDir)

	maxIterations := 25

	// Reflection guard: stop if the model keeps producing tool calls that all
	// fail for several rounds in a row (e.g. repeating an edit the lint gate
	// rejects). Prevents burning iterations on a stuck self-correction loop.
	consecutiveErrorRounds := 0
	const maxErrorRounds = 3

	// ── Hook system: load from project + skill source ──
	hookRegistry := hooks.NewRegistry()
	hookRegistry.Load(workDir, "") // "" = all sources
	hookRegistry.Execute(hooks.HookSessionStart, map[string]string{"WORK_DIR": workDir})

	// ── Permission snapshot (immutable for this session) ──
	permissions := hooks.CapturePermissions(workDir)
	_ = permissions // used in tool execution below

	// ── Compaction config ──
	compactCfg := CompactConfig{ContextWindow: 200000}

	// ── Detect project type ──
	project := DetectProject(workDir)
	projectPrompt := project.ToPrompt()
	eventCh <- Event{Type: "status", Data: fmt.Sprintf("Project: %s (%s, %d files)", project.Name, project.Type, project.FileCount)}

	// ── Load project context (CLAUDE.md, AGENTS.md, skills) ──
	projectCtx := LoadProjectContext(workDir)
	skills := LoadSkills(workDir)
	mcpConfig := LoadMCPConfig(workDir)

	// ── Long-term memory: load index + top relevant entries for this turn ──
	// Computed once before the iteration loop — the snippet only depends
	// on the first user message, so recomputing per-iteration would just
	// defeat the prompt cache.
	memoryContext := BuildMemoryContext(workDir, messages)

	// ── Process slash commands ──
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		var lastText string
		json.Unmarshal(lastMsg.Content, &lastText)
		if IsSlashCommand(lastText) {
			processed, err := ProcessSlashCommand(lastText, skills)
			if err != nil {
				eventCh <- Event{Type: "error", Data: err.Error()}
				return
			}
			// Direct output commands — don't send to LLM
			if processed == "[CLEAR_CHAT]" || processed == "[SHOW_MODEL_SELECTOR]" {
				eventCh <- Event{Type: "command", Data: processed}
				return
			}
			if processed == "[COMPACT_CONTEXT]" {
				eventCh <- Event{Type: "status", Data: "Compressing context..."}
			}
			// /help → return directly, no LLM needed
			if strings.HasPrefix(lastText, "/help") {
				eventCh <- Event{Type: "text", Data: processed}
				eventCh <- Event{Type: "done", Data: nil}
				return
			}
			// Replace last message with processed skill prompt
			messages[len(messages)-1] = types.Message{
				Role:    "user",
				Content: mustJSON(processed),
			}
			eventCh <- Event{Type: "status", Data: "Skill loaded: " + lastText}
		}
	}

	// ── Connect MCP servers ──
	if mcpConfig != "" {
		count, _ := ConnectMCPServers(workDir)
		if count > 0 {
			eventCh <- Event{Type: "status", Data: fmt.Sprintf("Connected to %d MCP servers", count)}
		}
	}

	// Mention skills as a single pointer line — do NOT enumerate them and do
	// NOT inline their content. Two separate failures were observed driving an
	// open model (Qwen3 via Ollama/SGLang) and confirmed by replaying captured
	// requests directly against the backend:
	//   1. Inlining every SKILL.md balloons the prompt to ~700KB (~180K tokens),
	//      overflowing a local model's ~32K context so it loses the task/tools.
	//   2. Even a compact name+description index of ~100 skills SUPPRESSES tool
	//      calling: the model reads the list as a menu and answers with prose
	//      ("here is the code…") instead of emitting a tool_use — and then
	//      hallucinates that it created the file. Dropping the enumeration and
	//      keeping a one-line pointer restores reliable tool calls.
	// Skills stay fully usable: the user invokes one with /<name> and
	// ProcessSlashCommand (above) expands its full prompt before the LLM runs,
	// so the model never needs to see the catalog to use it.
	skillText := ""
	if len(skills) > 0 {
		skillText = fmt.Sprintf("\n\n## Skills\n%d task skills are available; the user invokes one by typing /<name>. Skills are not tools — never try to call them. Just do the work with the tools above.", len(skills))
		eventCh <- Event{Type: "status", Data: fmt.Sprintf("Loaded %d skills", len(skills))}
	}
	if projectCtx != "" {
		eventCh <- Event{Type: "status", Data: "Project context loaded (CLAUDE.md)"}
	}
	if mcpConfig != "" {
		eventCh <- Event{Type: "status", Data: "MCP config detected"}
	}

	for i := 0; i < maxIterations; i++ {
		// ── Context compression ──
		tokenEstimate := EstimateMessageTokens(messages)
		if ShouldCompact(compactCfg, tokenEstimate) && len(messages) >= minMessagesForCompact {
			eventCh <- Event{Type: "status", Data: fmt.Sprintf("Compacting context (~%dk tokens, %d messages)...", tokenEstimate/1000, len(messages))}

			// Try LLM-based compaction first
			compacted, err := CompactMessages(ctx, provider, model, messages)
			if err != nil {
				compactCfg.CompactFailures++
				log.Printf("[Compact] LLM compact failed (%d/%d): %v — falling back to snip", compactCfg.CompactFailures, maxCompactFailures, err)

				// Snip fallback: keep first 2 + last 4, summarize middle inline
				if len(messages) > 8 {
					var middleSummary string
					for _, m := range messages[2 : len(messages)-4] {
						var text string
						json.Unmarshal(m.Content, &text)
						if len(text) > 100 {
							text = text[:100] + "..."
						}
						if text != "" {
							middleSummary += fmt.Sprintf("[%s] %s\n", m.Role, text)
						}
					}
					snipped := make([]types.Message, 0)
					snipped = append(snipped, messages[:2]...)
					snipped = append(snipped, types.Message{Role: "user", Content: mustJSON("[Context Summary]\n" + middleSummary)})
					snipped = append(snipped, messages[len(messages)-4:]...)
					messages = snipped
				}
			} else {
				messages = compacted
				compactCfg.CompactFailures = 0
			}
			eventCh <- Event{Type: "status", Data: fmt.Sprintf("Compacted to %d messages", len(messages))}
		}

		// Normalize messages before API call
		messages = NormalizeMessages(messages)

		// RAG: search project for relevant context based on last user message
		ragContext := ""
		if i == 0 && len(messages) > 0 { // only on first iteration
			lastUser := ""
			for j := len(messages) - 1; j >= 0; j-- {
				if messages[j].Role == "user" {
					json.Unmarshal(messages[j].Content, &lastUser)
					break
				}
			}
			if lastUser != "" {
				ragResults := RAGSearch(workDir, lastUser, 3)
				ragContext = FormatRAGContext(ragResults)
			}
		}

		// Build request with full context
		sysPrompt := buildSystemPrompt(responseLang) + projectPrompt + projectCtx + skillText + ragContext + memoryContext
		req := &types.MessagesRequest{
			Model:     model,
			System:    mustJSON([]map[string]string{{"type": "text", "text": sysPrompt}}),
			Messages:  messages,
			Tools:     tools,
			MaxTokens: 8192,
		}

		// Call LLM (with retry)
		eventCh <- Event{Type: "status", Data: fmt.Sprintf("Thinking... (iteration %d/%d, ~%dk tokens)", i+1, maxIterations, tokenEstimate/1000)}

		// Liveness: heartbeat elapsed-time + output size every second. Started
		// BEFORE StreamMessage on purpose — for a slow/cold local model the
		// longest dead air is inside StreamMessage itself (Ollama blocks the
		// HTTP response until a 23GB model finishes loading + prefill), well
		// before the first delta. outChars stays 0 during load, so the client
		// shows "Ns · 0 chars" — exactly the proof-of-life we want. Idempotent
		// stop, called on every exit path before eventCh closes.
		var outChars int64
		stopHeartbeat := startHeartbeat(eventCh, &outChars)

		var ch <-chan types.SSEEvent
		var err error
		for retry := 0; retry < 3; retry++ {
			ch, err = provider.StreamMessage(ctx, req, nil)
			if err == nil {
				break
			}
			if retry < 2 {
				eventCh <- Event{Type: "status", Data: fmt.Sprintf("Retrying... (%d/3): %s", retry+1, err.Error())}
				select {
				case <-ctx.Done():
					stopHeartbeat()
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
		if err != nil {
			stopHeartbeat()
			eventCh <- Event{Type: "error", Data: fmt.Sprintf("Failed after 3 retries: %s", err.Error())}
			return
		}

		// Collect response
		var textContent string
		var toolUses []toolUseBlock
		currentText := ""
		var currentTool *toolUseBlock

		for event := range ch {
			switch event.Type {
			case "content_block_start":
				var block struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				json.Unmarshal(event.ContentBlock, &block)

				if block.Type == "thinking" {
					// Thinking block — stream to UI
					eventCh <- Event{Type: "status", Data: "Thinking..."}
				} else if block.Type == "text" {
					currentText = ""
				} else if block.Type == "tool_use" {
					currentTool = &toolUseBlock{ID: block.ID, Name: block.Name}
					eventCh <- Event{Type: "tool_start", Data: map[string]string{
						"id": block.ID, "name": block.Name,
					}}
				}

			case "content_block_delta":
				var delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				}
				json.Unmarshal(event.Delta, &delta)

				if delta.Type == "thinking_delta" {
					// Stream thinking to UI as dimmed text
					var thinkDelta struct {
						Thinking string `json:"thinking"`
					}
					json.Unmarshal(event.Delta, &thinkDelta)
					if thinkDelta.Thinking != "" {
						atomic.AddInt64(&outChars, int64(len(thinkDelta.Thinking)))
						eventCh <- Event{Type: "thinking", Data: thinkDelta.Thinking}
					}
				} else if delta.Type == "text_delta" {
					currentText += delta.Text
					atomic.AddInt64(&outChars, int64(len(delta.Text)))
					eventCh <- Event{Type: "text", Data: delta.Text}
				} else if delta.Type == "input_json_delta" && currentTool != nil {
					currentTool.InputRaw += delta.PartialJSON
				}

			case "content_block_stop":
				if currentTool != nil {
					currentTool.Input = json.RawMessage(currentTool.InputRaw)
					toolUses = append(toolUses, *currentTool)
					currentTool = nil
				} else if currentText != "" {
					textContent += currentText
				}

			case "message_stop":
				// done with this LLM call
			}
		}

		// Generation finished for this iteration — stop the liveness ticker
		// before tool execution (tools emit their own progress) and before any
		// return path that would close eventCh.
		stopHeartbeat()

		// ── No tool calls → done ──
		if len(toolUses) == 0 {
			// ── Memory hooks: extract durable memories from this
			//    conversation and (maybe) consolidate. Both run in
			//    background goroutines and their failures are logged,
			//    never surfaced to the user. We only hook normal
			//    termination — the max-iterations branch below is a
			//    failure mode that would feed noisy data to extraction.
			ExtractMemoriesAsync(ctx, provider, model, workDir, messages)
			MaybeConsolidateAsync(ctx, provider, model, workDir)

			eventCh <- Event{Type: "done", Data: map[string]interface{}{
				"iterations":    i + 1,
				"tokenEstimate": tokenEstimate,
				"project":       project.Type,
			}}
			return
		}

		// ── Build assistant message with tool_use blocks ──
		var assistantContent []map[string]interface{}
		if textContent != "" {
			assistantContent = append(assistantContent, map[string]interface{}{
				"type": "text", "text": textContent,
			})
		}
		for _, tu := range toolUses {
			assistantContent = append(assistantContent, map[string]interface{}{
				"type": "tool_use", "id": tu.ID, "name": tu.Name, "input": json.RawMessage(tu.InputRaw),
			})
		}
		messages = append(messages, types.Message{
			Role:    "assistant",
			Content: mustJSON(assistantContent),
		})

		// ── Partition tools into concurrent-safe vs serial ──
		var concurrentTools, serialTools []toolUseBlock
		for _, tu := range toolUses {
			inputMap := make(map[string]interface{})
			json.Unmarshal(tu.Input, &inputMap)
			if IsConcurrencySafe(tu.Name, inputMap) {
				concurrentTools = append(concurrentTools, tu)
			} else {
				serialTools = append(serialTools, tu)
			}
		}
		if len(concurrentTools) > 1 {
			log.Printf("[Agent] Parallel: %d concurrent + %d serial", len(concurrentTools), len(serialTools))
		}

		// ── Execute tools and collect results ──
		var toolResults []map[string]interface{}
		// First: run concurrent-safe tools in parallel
		if len(concurrentTools) > 1 {
			type toolResultEntry struct {
				idx    int
				result map[string]interface{}
				event  Event
			}
			resultCh := make(chan toolResultEntry, len(concurrentTools))

			for idx, tu := range concurrentTools {
				go func(i int, t toolUseBlock) {
					hookRegistry.Execute(hooks.HookPreToolUse, map[string]string{
						"TOOL_NAME": t.Name, "WORK_DIR": workDir,
					})
					r, isErr := ExecuteTool(t.Name, t.Input, workDir)
					hookRegistry.Execute(hooks.HookPostToolUse, map[string]string{
						"TOOL_NAME": t.Name, "WORK_DIR": workDir,
						"TOOL_ERROR": fmt.Sprintf("%v", isErr),
					})
					resultCh <- toolResultEntry{
						idx: i,
						result: map[string]interface{}{
							"type": "tool_result", "tool_use_id": t.ID,
							"content": r, "is_error": isErr,
						},
						event: Event{Type: "tool_result", Data: map[string]interface{}{
							"id": t.ID, "name": t.Name, "result": truncateStr(r, 2000), "isError": isErr,
						}},
					}
				}(idx, tu)
			}

			// Collect parallel results
			collected := make([]toolResultEntry, len(concurrentTools))
			for i := 0; i < len(concurrentTools); i++ {
				entry := <-resultCh
				collected[entry.idx] = entry
			}
			for _, entry := range collected {
				eventCh <- entry.event
				toolResults = append(toolResults, entry.result)
			}
		} else {
			// Run single concurrent tool normally (falls through to serial loop)
			serialTools = append(concurrentTools, serialTools...)
		}

		// Then: run serial tools one by one
		for _, tu := range serialTools {
			log.Printf("[Agent] Executing: %s", tu.Name)

			// ── Pre-tool hook ──
			hookRegistry.Execute(hooks.HookPreToolUse, map[string]string{
				"TOOL_NAME": tu.Name, "WORK_DIR": workDir,
			})

			// ── Permission check (snapshot + legacy) ──
			permDecision := permissions.Decide(tu.Name, string(tu.InputRaw))

			permCfg := DefaultPermissionConfig()
			permCfg.AutoApprove = "moderate"
			allowed, permReason, dangerLevel := CheckPermission(tu.Name, tu.Input, workDir, permCfg)

			// Snapshot decision overrides if explicit
			if permDecision == "deny" {
				allowed = false
				permReason = "Denied by permission rule"
			} else if permDecision == "allow" {
				allowed = true
			} else if permDecision == "ask" && allowed {
				// Tool was allowed by legacy check but snapshot says "ask"
				// Persist this as an allow rule for future sessions
				hooks.PersistAllowRule(workDir, tu.Name, "")
			}

			// Show tool input to client
			var inputPreview interface{}
			json.Unmarshal(tu.Input, &inputPreview)
			eventCh <- Event{Type: "tool_input", Data: map[string]interface{}{
				"id": tu.ID, "name": tu.Name, "input": inputPreview,
				"danger": string(dangerLevel),
			}}

			if !allowed {
				eventCh <- Event{Type: "tool_result", Data: map[string]interface{}{
					"id": tu.ID, "name": tu.Name,
					"result": fmt.Sprintf("[BLOCKED] %s", permReason), "isError": true,
				}}
				toolResults = append(toolResults, map[string]interface{}{
					"type": "tool_result", "tool_use_id": tu.ID,
					"content": fmt.Sprintf("Permission denied: %s", permReason), "is_error": true,
				})
				continue
			}

			result, isError := ExecuteTool(tu.Name, tu.Input, workDir)

			// ── Post-tool hook ──
			hookRegistry.Execute(hooks.HookPostToolUse, map[string]string{
				"TOOL_NAME": tu.Name, "WORK_DIR": workDir,
				"TOOL_RESULT": truncateStr(result, 500),
				"TOOL_ERROR":  fmt.Sprintf("%v", isError),
			})

			// Send result to client
			eventCh <- Event{Type: "tool_result", Data: map[string]interface{}{
				"id": tu.ID, "name": tu.Name, "result": truncateStr(result, 2000), "isError": isError,
			}}

			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": tu.ID,
				"content":     result,
				"is_error":    isError,
			})
		}

		// ── Add tool results as user message ──
		messages = append(messages, types.Message{
			Role:    "user",
			Content: mustJSON(toolResults),
		})

		// ── Reflection guard ──
		// A round where every tool errored counts as a failed round; any
		// success resets the counter. Too many failed rounds in a row means
		// the model is stuck (e.g. repeating an edit the lint gate rejects),
		// so stop instead of looping all the way to maxIterations.
		if allToolsErrored(toolResults) {
			consecutiveErrorRounds++
		} else {
			consecutiveErrorRounds = 0
		}
		if consecutiveErrorRounds >= maxErrorRounds {
			eventCh <- Event{Type: "error", Data: fmt.Sprintf(
				"Stopped after %d consecutive failed tool rounds — the model appears "+
					"stuck repeating a failing action. Try rephrasing the request.",
				consecutiveErrorRounds)}
			hookRegistry.Execute(hooks.HookSessionEnd, map[string]string{"WORK_DIR": workDir})
			return
		}

		eventCh <- Event{Type: "status", Data: fmt.Sprintf("Iteration %d/%d — %d tools executed", i+1, maxIterations, len(toolUses))}
	}

	hookRegistry.Execute(hooks.HookSessionEnd, map[string]string{"WORK_DIR": workDir})
	eventCh <- Event{Type: "error", Data: "Max iterations reached"}
}

type toolUseBlock struct {
	ID       string
	Name     string
	InputRaw string
	Input    json.RawMessage
}

// allToolsErrored reports whether every tool result in a round is an error
// (and there is at least one). Used by the reflection guard in RunLoop.
func allToolsErrored(toolResults []map[string]interface{}) bool {
	if len(toolResults) == 0 {
		return false
	}
	for _, r := range toolResults {
		if e, ok := r["is_error"].(bool); !ok || !e {
			return false
		}
	}
	return true
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
