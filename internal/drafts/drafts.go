// Package drafts implements the draft and revision HTTP endpoints — the
// multi-turn revision flow introduced in article 3.
//
// The endpoints:
//
//	POST /api/drafts                   create a draft
//	GET  /api/drafts/{id}              fetch a draft + its revisions
//	POST /api/drafts/{id}/revisions    append a revision turn
//
// Each revision turn replays the full prior conversation (the draft's initial
// content plus every earlier revision) as alternating user/assistant messages,
// then appends the new user prompt. The messages array is the model's only
// memory — this package is a small demonstration of that fact.
package drafts

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"crawshaw.io/sqlite/sqlitex"
	"crawshaw.io/sqlite/sqlitex/orm"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/go-chi/chi/v5"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"

	"github.com/riyaz-ali/inkwell/internal/domain"
	"github.com/riyaz-ali/inkwell/internal/util"
)

const systemPrompt = "You are a writing assistant. Help the user improve their draft. Return only the revised text — no preamble, no explanations."

// ---------- POST /api/drafts ----------

// CreateRequest is the JSON body accepted by POST /api/drafts.
type CreateRequest struct {
	Content string `json:"content"`
}

// CreateResponse is the JSON body returned by POST /api/drafts.
type CreateResponse struct {
	DraftID int    `json:"draft_id"`
	Content string `json:"content"`
}

// Create persists the initial content of a new draft. No model call happens
// here — creation is a pure storage operation. The first revision is
// requested separately via POST /api/drafts/{id}/revisions.
func Create(pool *sqlitex.Pool) util.HandlerFunc[CreateRequest, CreateResponse] {
	return func(ctx context.Context, req CreateRequest) (*CreateResponse, error) {
		if req.Content == "" {
			return nil, errors.New("content is required")
		}

		conn := pool.Get(ctx)
		defer pool.Put(conn)

		rows, err := orm.Exec(conn, domain.InsertDraft(req.Content))
		if err != nil {
			return nil, errors.Wrap(err, "create draft")
		}

		zerolog.Ctx(ctx).Info().Int("draft_id", rows[0].ID).Msg("draft created")
		return &CreateResponse{DraftID: rows[0].ID, Content: rows[0].Content}, nil
	}
}

// ---------- GET /api/drafts/{id} ----------

// GetResponse is the JSON body returned by GET /api/drafts/{id}.
type GetResponse struct {
	Draft     *domain.Draft      `json:"draft"`
	Revisions []*domain.Revision `json:"revisions"`
}

// Get returns a draft's current state — initial content plus every revision
// applied to it so far. The frontend uses this to restore a session.
func Get(pool *sqlitex.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := zerolog.Ctx(ctx)

		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "invalid draft id", http.StatusBadRequest)
			return
		}

		conn := pool.Get(ctx)
		defer pool.Put(conn)

		draft, err := orm.FetchOne(conn, domain.GetDraftByID(int64(id)))
		if err != nil {
			log.Error().Err(err).Send()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if draft == nil {
			http.Error(w, "draft not found", http.StatusNotFound)
			return
		}

		revisions, err := orm.FetchMany(conn, domain.ListRevisionsForDraft(id))
		if err != nil {
			log.Error().Err(err).Send()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GetResponse{Draft: draft, Revisions: revisions})
	}
}

// ---------- POST /api/drafts/{id}/revisions ----------

// ReviseRequest is the JSON body accepted by POST /api/drafts/{id}/revisions.
type ReviseRequest struct {
	Prompt string `json:"prompt"`
}

// ReviseResponse is the JSON body returned by POST /api/drafts/{id}/revisions.
type ReviseResponse struct {
	RevisionID int    `json:"revision_id"`
	Completion string `json:"completion"`
	Turn       int    `json:"turn"`
}

// Revise adds a new turn to a draft's revision history. It loads the draft
// and every prior revision, replays them as a conversation, appends the new
// user prompt, calls Claude, and saves the assistant's reply as a revision.
func Revise(pool *sqlitex.Pool, ai *anthropic.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := zerolog.Ctx(ctx)

		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "invalid draft id", http.StatusBadRequest)
			return
		}

		var req ReviseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Error().Err(err).Msg("decode revise body")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp, err := revise(ctx, pool, ai, id, req)
		if err != nil {
			log.Error().Err(err).Send()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// revise is the business logic behind POST /api/drafts/{id}/revisions,
// factored out of the HTTP shell so it reads top-to-bottom without the
// param-extraction boilerplate.
func revise(ctx context.Context, pool *sqlitex.Pool, ai *anthropic.Client, draftID int, req ReviseRequest) (*ReviseResponse, error) {
	log := zerolog.Ctx(ctx)

	if req.Prompt == "" {
		return nil, errors.New("prompt is required")
	}

	conn := pool.Get(ctx)
	defer pool.Put(conn)

	draft, err := orm.FetchOne(conn, domain.GetDraftByID(int64(draftID)))
	if err != nil {
		return nil, errors.Wrap(err, "fetch draft")
	}
	if draft == nil {
		return nil, errors.New("draft not found")
	}

	history, err := orm.FetchMany(conn, domain.ListRevisionsForDraft(draftID))
	if err != nil {
		return nil, errors.Wrap(err, "load history")
	}

	messages := buildMessages(draft.Content, history, req.Prompt)

	msg, err := ai.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 1024,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:  messages,
	})
	if err != nil {
		return nil, errors.Wrap(err, "anthropic")
	}
	if len(msg.Content) == 0 {
		return nil, errors.New("empty completion from model")
	}

	completion := msg.Content[0].Text

	rows, err := orm.Exec(conn, domain.InsertRevision(&domain.Revision{
		DraftID:    draftID,
		Prompt:     req.Prompt,
		Completion: completion,
	}))
	if err != nil {
		return nil, errors.Wrap(err, "save revision")
	}

	turn := len(history) + 1
	log.Info().
		Int("draft_id", draftID).
		Int("revision_id", rows[0].ID).
		Int("turn", turn).
		Int("tokens_in", int(msg.Usage.InputTokens)).
		Int("tokens_out", int(msg.Usage.OutputTokens)).
		Msg("revision saved")

	return &ReviseResponse{
		RevisionID: rows[0].ID,
		Completion: completion,
		Turn:       turn,
	}, nil
}

// buildMessages reconstructs the conversation for the next turn.
//
// The first user message carries both the original draft content and the
// first instruction — this mirrors how article 1's single-turn handler
// packed them together, and keeps the model's view of the conversation
// consistent as it grows.
//
// Every subsequent revision contributes an (assistant completion → user
// prompt) pair. The newPrompt is appended last as the turn the model is
// about to respond to.
func buildMessages(content string, history []*domain.Revision, newPrompt string) []anthropic.MessageParam {
	// No prior revisions: one user message, content + prompt.
	if len(history) == 0 {
		return []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(content + "\n\n" + newPrompt)),
		}
	}

	messages := make([]anthropic.MessageParam, 0, len(history)*2+1)

	// First turn: original draft paired with the first prompt.
	messages = append(messages,
		anthropic.NewUserMessage(anthropic.NewTextBlock(content+"\n\n"+history[0].Prompt)),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock(history[0].Completion)),
	)

	// Remaining revisions: just prompt → completion pairs.
	for _, rev := range history[1:] {
		messages = append(messages,
			anthropic.NewUserMessage(anthropic.NewTextBlock(rev.Prompt)),
			anthropic.NewAssistantMessage(anthropic.NewTextBlock(rev.Completion)),
		)
	}

	// Final user turn: the new prompt, awaiting a reply.
	messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(newPrompt)))
	return messages
}
