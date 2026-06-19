package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	// <function=NAME> ... <parameter=KEY>VALUE</parameter> ... </function>
	leakFuncRe  = regexp.MustCompile(`(?s)<function=([A-Za-z_][A-Za-z0-9_]*)\s*>(.*?)</function>`)
	leakParamRe = regexp.MustCompile(`(?s)<parameter=([A-Za-z_][A-Za-z0-9_]*)\s*>(.*?)</parameter>`)
	// <tool_call>{"name":..,"arguments":{..}}</tool_call>
	leakJSONRe = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)
)

// recoverLeakedToolCalls salvages tool calls a model emitted as plain text
// instead of in the parseable format the backend recognizes (observed with qwen
// via Ollama, e.g. "<function=Read><parameter=file_path>foo.py</parameter>
// </function>"). It returns the recovered calls and the text with the leaked
// syntax stripped. Returns nil when nothing looks like a leaked call — the
// caller still validates names against the real tool set before executing.
func recoverLeakedToolCalls(text string) (calls []toolUseBlock, cleaned string) {
	cleaned = text
	n := 0
	add := func(name string, raw json.RawMessage) {
		calls = append(calls, toolUseBlock{
			ID:       fmt.Sprintf("call_recovered_%d", n),
			Name:     name,
			Input:    raw,
			InputRaw: string(raw),
		})
		n++
	}

	// Format A: <function=NAME>...<parameter=k>v</parameter>...</function>
	for _, m := range leakFuncRe.FindAllStringSubmatch(text, -1) {
		input := map[string]string{}
		for _, p := range leakParamRe.FindAllStringSubmatch(m[2], -1) {
			input[p[1]] = strings.TrimSpace(p[2])
		}
		raw, _ := json.Marshal(input)
		add(m[1], raw)
	}
	cleaned = leakFuncRe.ReplaceAllString(cleaned, "")

	// Format B: <tool_call>{json}</tool_call>
	for _, m := range leakJSONRe.FindAllStringSubmatch(text, -1) {
		var tc struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal([]byte(m[1]), &tc) == nil && tc.Name != "" {
			raw := tc.Arguments
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			add(tc.Name, raw)
		}
	}
	cleaned = leakJSONRe.ReplaceAllString(cleaned, "")

	// Drop any orphan tool_call wrappers left behind.
	cleaned = strings.NewReplacer("<tool_call>", "", "</tool_call>", "").Replace(cleaned)
	return calls, strings.TrimSpace(cleaned)
}
