package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Session represents a saved chat conversation.
type Session struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Workspace string           `json:"workspace"` // project directory path
	Messages  []SessionMessage `json:"messages"`
	Provider  string           `json:"provider"`
	Model     string           `json:"model"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Turns     int              `json:"turns"`
}

// SessionMessage is a message in a session (user, assistant, or tool).
type SessionMessage struct {
	Role       string      `json:"role"` // "user", "assistant", "tool"
	Content    string      `json:"content"`
	ToolName   string      `json:"toolName,omitempty"`
	ToolInput  interface{} `json:"toolInput,omitempty"`
	ToolResult string      `json:"toolResult,omitempty"`
	IsError    bool        `json:"isError,omitempty"`
	Timestamp  time.Time   `json:"timestamp"`
}

// SessionSummary is a lightweight view for listing sessions.
type SessionSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	Workspace string    `json:"workspace"`
	Turns     int       `json:"turns"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Workspace represents a project directory.
type Workspace struct {
	Path     string `json:"path"`
	Name     string `json:"name"`     // folder name
	Sessions int    `json:"sessions"` // number of sessions
}


// SessionStore manages session persistence.
type SessionStore struct {
	mu  sync.RWMutex
	dir string
}

func NewSessionStore(baseDir string) *SessionStore {
	dir := filepath.Join(baseDir, "sessions")
	os.MkdirAll(dir, 0755)
	return &SessionStore{dir: dir}
}

// workspaceDir returns the directory for a workspace, creating it if needed.
func (s *SessionStore) workspaceDir(workspace string) string {
	// Convert path to safe directory name: D:\git\projectA → D--git--projectA
	safe := strings.ReplaceAll(workspace, ":", "")
	safe = strings.ReplaceAll(safe, "\\", "--")
	safe = strings.ReplaceAll(safe, "/", "--")
	dir := filepath.Join(s.dir, safe)
	os.MkdirAll(dir, 0755)
	return dir
}

// ListWorkspaces returns all workspaces that have sessions.
func (s *SessionStore) ListWorkspaces() []Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}

	var workspaces []Workspace
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Count sessions in this workspace
		files, _ := os.ReadDir(filepath.Join(s.dir, e.Name()))
		count := 0
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				count++
			}
		}
		if count == 0 {
			continue
		}
		// Reconstruct path from safe name
		path := strings.ReplaceAll(e.Name(), "--", "/")
		name := filepath.Base(path)
		workspaces = append(workspaces, Workspace{
			Path:     path,
			Name:     name,
			Sessions: count,
		})
	}
	return workspaces
}

// List returns sessions for a specific workspace, sorted by updated time.
func (s *SessionStore) List(workspace string) []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := s.workspaceDir(workspace)
	files, err := os.ReadDir(dir)
	if err != nil {
		// Always return a non-nil slice — encoding/json emits `null` for a nil
		// slice, which crashes a frontend that does `sessions.length` after
		// switching to a workspace with no session history yet.
		return []SessionSummary{}
	}

	sessions := []SessionSummary{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}

		var sess Session
		if json.Unmarshal(data, &sess) != nil {
			continue
		}

		preview := ""
		for _, m := range sess.Messages {
			if m.Role == "user" {
				preview = m.Content
			}
		}
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}

		sessions = append(sessions, SessionSummary{
			ID:        sess.ID,
			Title:     sess.Title,
			Preview:   preview,
			Workspace: sess.Workspace,
			Turns:     sess.Turns,
			Provider:  sess.Provider,
			Model:     sess.Model,
			UpdatedAt: sess.UpdatedAt,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions
}

// ListAll returns all sessions across all workspaces.
func (s *SessionStore) ListAll() []SessionSummary {
	workspaces := s.ListWorkspaces()
	all := []SessionSummary{} // non-nil for JSON `[]` rather than `null`
	for _, ws := range workspaces {
		all = append(all, s.List(ws.Path)...)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})
	return all
}

// Get loads a session by ID, searching across all workspaces.
func (s *SessionStore) Get(id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Search all workspace directories
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(s.dir, e.Name(), id+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		return &sess, nil
	}
	return nil, fmt.Errorf("session not found: %s", id)
}

// Save persists a session to disk under its workspace directory.
func (s *SessionStore) Save(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess.ID == "" {
		sess.ID = fmt.Sprintf("sess_%s", time.Now().Format("20060102-150405"))
	}
	if sess.Workspace == "" {
		sess.Workspace, _ = os.Getwd()
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	sess.UpdatedAt = time.Now()

	// Auto-generate title from first user message
	if sess.Title == "" {
		for _, m := range sess.Messages {
			if m.Role == "user" && m.Content != "" {
				title := m.Content
				if len(title) > 50 {
					title = title[:50] + "..."
				}
				sess.Title = title
				break
			}
		}
		if sess.Title == "" {
			sess.Title = "New Chat"
		}
	}

	// Count turns
	turns := 0
	for _, m := range sess.Messages {
		if m.Role == "user" {
			turns++
		}
	}
	sess.Turns = turns

	dir := s.workspaceDir(sess.Workspace)
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sess.ID+".json"), data, 0644)
}

// Delete removes a session.
func (s *SessionStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(s.dir, e.Name(), id+".json")
		if err := os.Remove(path); err == nil {
			return nil
		}
	}
	return fmt.Errorf("session not found: %s", id)
}

// Rename changes a session's title.
func (s *SessionStore) Rename(id, title string) error {
	sess, err := s.Get(id)
	if err != nil {
		return err
	}
	sess.Title = title
	return s.Save(sess)
}
