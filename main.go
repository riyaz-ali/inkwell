package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"crawshaw.io/sqlite"
	"github.com/anthropics/anthropic-sdk-go"
	tools "github.com/riyaz-ali/tools.sql"
)

// Draft represents a saved completion in the database.
type Draft struct {
	ID      int64  `db:"id"`
	Content string `db:"content"`
}

// draftInsert returns an insert query for a given completion text.
func draftInsert(content string) tools.I[Draft, string] {
	return tools.I[Draft, string]{
		QueryStr: `INSERT INTO drafts (content) VALUES (?) RETURNING id, content`,
		ArgSet:   []string{content},
		Bind: func(stmt *sqlite.Stmt, s string) error {
			stmt.BindText(1, s)
			return nil
		},
		Val: func(stmt *sqlite.Stmt) (*Draft, error) {
			return tools.ScanAs[Draft](stmt)
		},
	}
}

// server holds shared application state.
type server struct {
	db  *sqlite.Conn
	ai  *anthropic.Client
}

func (s *server) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Content string `json:"content"`
		Prompt  string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Content == "" || req.Prompt == "" {
		http.Error(w, `{"error":"content and prompt are required"}`, http.StatusBadRequest)
		return
	}

	userMessage := req.Content + "\n\n" + req.Prompt

	msg, err := s.ai.Messages.New(r.Context(), anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are a writing assistant. Help the user improve their draft. Return only the revised text — no preamble, no explanations."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
		},
	})
	if err != nil {
		log.Printf("anthropic error: %v", err)
		http.Error(w, `{"error":"completion failed"}`, http.StatusInternalServerError)
		return
	}

	completion := msg.Content[0].Text

	rows, err := tools.Exec(s.db, draftInsert(completion))
	if err != nil {
		log.Printf("db error: %v", err)
		http.Error(w, `{"error":"failed to save draft"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"completion": completion,
		"draft_id":   rows[0].ID,
	})
}

func main() {
	dbPath := getenv("DB_PATH", "inkwell.db")
	port := getenv("PORT", "8080")

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Fatal("ANTHROPIC_API_KEY is required")
	}

	db, err := openDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ai := anthropic.NewClient()
	s := &server{
		db: db,
		ai: &ai,
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("web")))
	mux.HandleFunc("/api/complete", s.handleComplete)

	addr := fmt.Sprintf(":%s", port)
	log.Printf("inkwell listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
