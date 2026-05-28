package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
)

// runChat is AniClew's built-in terminal client. It connects to a running
// AniClew server's /api/agent endpoint and renders the streamed agent loop in
// the terminal — a CLI experience that needs no external tool (claude/codex)
// and makes no outbound internet call, so it works inside an air-gapped network
// where those proprietary CLIs cannot be installed.
//
//	aniclew chat                 # interactive REPL against http://localhost:4000
//	aniclew chat -p "fix the bug" # one-shot
//	aniclew chat -url http://host:4000 -workdir /path/to/project
func runChat(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	url := fs.String("url", "http://localhost:4000", "AniClew server URL")
	workdir := fs.String("workdir", "", "Working directory for the agent (default: current dir)")
	lang := fs.String("lang", "auto", "Response language (auto, en, ko, ja, zh)")
	prompt := fs.String("p", "", "One-shot prompt (non-interactive); omit for interactive REPL")
	provider := fs.String("provider", "", "Optionally switch the server's provider before chatting")
	model := fs.String("model", "", "Optionally switch the server's model before chatting")
	noColor := fs.Bool("no-color", false, "Disable ANSI colors")
	showThinking := fs.Bool("show-thinking", false, "Show the model's reasoning (dimmed)")
	quiet := fs.Bool("quiet", false, "Hide status lines (project detection, iterations, etc.)")
	fs.Parse(args)

	c := &chatClient{
		base:         strings.TrimRight(*url, "/"),
		lang:         *lang,
		color:        !*noColor,
		showThinking: *showThinking,
		showStatus:   !*quiet,
		http:         &http.Client{Timeout: 0}, // agent turns can be long; no client deadline
	}
	if *workdir != "" {
		c.workDir = *workdir
	} else {
		c.workDir, _ = os.Getwd()
	}

	// Verify the server is reachable early with a clear message.
	if err := c.ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot reach AniClew at %s — is the server running?\n  (%v)\n", c.base, err)
		os.Exit(1)
	}

	// Optional provider/model switch.
	if *provider != "" && *model != "" {
		if err := c.setConfig(*provider, *model); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not switch model: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "%sModel set to %s/%s%s\n", c.dim(), *provider, *model, c.rst())
		}
	}

	if *prompt != "" {
		c.runOnce(*prompt)
		return
	}
	c.repl()
}

type chatClient struct {
	base         string
	workDir      string
	lang         string
	color        bool
	showThinking bool
	showStatus   bool
	http         *http.Client
	messages     []chatMsg
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ── ANSI helpers (no-ops when color is disabled) ──

func (c *chatClient) dim() string  { return c.code("\033[2m") }
func (c *chatClient) cyan() string { return c.code("\033[36m") }
func (c *chatClient) ylw() string  { return c.code("\033[33m") }
func (c *chatClient) red() string  { return c.code("\033[31m") }
func (c *chatClient) rst() string  { return c.code("\033[0m") }
func (c *chatClient) code(s string) string {
	if c.color {
		return s
	}
	return ""
}

func (c *chatClient) ping() error {
	resp, err := c.http.Get(c.base + "/api/config")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *chatClient) setConfig(provider, model string) error {
	body, _ := json.Marshal(map[string]string{"provider": provider, "model": model})
	req, _ := http.NewRequest("PUT", c.base+"/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *chatClient) runOnce(prompt string) {
	c.messages = append(c.messages, chatMsg{Role: "user", Content: prompt})
	if _, err := c.streamTurn(); err != nil {
		fmt.Fprintf(os.Stderr, "\n%sError: %v%s\n", c.red(), err, c.rst())
		os.Exit(1)
	}
}

func (c *chatClient) repl() {
	fmt.Printf("%sAniClew chat — %s (workdir: %s)%s\n", c.cyan(), c.base, c.workDir, c.rst())
	fmt.Printf("%sType your message. Ctrl-C or 'exit' to quit.%s\n\n", c.dim(), c.rst())

	// Ctrl-C exits cleanly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		fmt.Printf("\n%sBye.%s\n", c.dim(), c.rst())
		os.Exit(0)
	}()

	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s›%s ", c.cyan(), c.rst())
		line, err := in.ReadString('\n')
		if err == io.EOF {
			fmt.Printf("\n%sBye.%s\n", c.dim(), c.rst())
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" || line == "/exit" {
			fmt.Printf("%sBye.%s\n", c.dim(), c.rst())
			return
		}

		c.messages = append(c.messages, chatMsg{Role: "user", Content: line})
		reply, err := c.streamTurn()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n%sError: %v%s\n", c.red(), err, c.rst())
			// Drop the user turn we could not answer so history stays consistent.
			c.messages = c.messages[:len(c.messages)-1]
			continue
		}
		c.messages = append(c.messages, chatMsg{Role: "assistant", Content: reply})
		fmt.Print("\n\n")
	}
}

// streamTurn POSTs the current message history to /api/agent and renders the
// SSE event stream. Returns the assistant's accumulated text for history.
func (c *chatClient) streamTurn() (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"messages":     c.messages,
		"workDir":      c.workDir,
		"responseLang": c.lang,
	})
	req, err := http.NewRequest("POST", c.base+"/api/agent", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var answer strings.Builder
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			c.renderLine(strings.TrimRight(line, "\r\n"), &answer)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return answer.String(), err
		}
	}
	return answer.String(), nil
}

func (c *chatClient) renderLine(line string, answer *strings.Builder) {
	data, ok := strings.CutPrefix(line, "data: ")
	if !ok || data == "" {
		return
	}
	var ev struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(data), &ev) != nil {
		return
	}

	switch ev.Type {
	case "text":
		var s string
		json.Unmarshal(ev.Data, &s)
		fmt.Print(s)
		answer.WriteString(s)

	case "thinking":
		if c.showThinking {
			var s string
			json.Unmarshal(ev.Data, &s)
			fmt.Printf("%s%s%s", c.dim(), s, c.rst())
		}

	case "status":
		if c.showStatus {
			var s string
			json.Unmarshal(ev.Data, &s)
			fmt.Printf("%s· %s%s\n", c.dim(), s, c.rst())
		}

	case "tool_start":
		var m map[string]string
		json.Unmarshal(ev.Data, &m)
		fmt.Printf("%s▸ %s%s\n", c.cyan(), m["name"], c.rst())

	case "tool_input":
		var m struct {
			Name   string `json:"name"`
			Input  any    `json:"input"`
			Danger string `json:"danger"`
		}
		json.Unmarshal(ev.Data, &m)
		if b, _ := json.Marshal(m.Input); len(b) > 0 {
			fmt.Printf("%s  %s%s\n", c.dim(), truncate(string(b), 200), c.rst())
		}

	case "tool_result":
		var m struct {
			Name    string `json:"name"`
			Result  string `json:"result"`
			IsError bool   `json:"isError"`
		}
		json.Unmarshal(ev.Data, &m)
		marker, col := "✓", c.dim()
		if m.IsError {
			marker, col = "✗", c.red()
		}
		fmt.Printf("%s  %s %s%s\n", col, marker, truncate(oneLine(m.Result), 200), c.rst())

	case "error":
		var s string
		json.Unmarshal(ev.Data, &s)
		fmt.Printf("\n%s✗ %s%s\n", c.red(), s, c.rst())

	case "session", "done", "stream_end", "command":
		// control frames — nothing to render
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
