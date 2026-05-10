// Package drafts implements the draft and revision HTTP endpoints.
//
// The endpoints:
//
//	POST /api/drafts                   create a draft
//	GET  /api/drafts/{id}              fetch a draft + its revisions
//	POST /api/drafts/{id}/revisions    append a revision turn (streams SSE)
//
// Article 5 turns the revision endpoint into a streaming response. Instead of
// blocking until Claude finishes, the handler relays each text chunk as a
// Server-Sent Event the moment the SDK surfaces it, then emits a final
// `done` event carrying the persisted revision metadata.
package drafts

import (
	"context"
	"encoding/json"
	"fmt"
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

// systemPrompts maps each writing mode to a system prompt that shapes how
// Claude approaches the revision. The key is the mode identifier sent by the
// client; the value is the full instruction given to the model as its role.
//
// All prompts share the same terminal instruction — return only the revised
// text — so the frontend can display the completion directly without stripping
// preamble. What differs is the editorial lens the model applies.
var systemPrompts = map[string]string{
	"academic": "You are an academic writing assistant. " +
		"Revise the draft with scholarly rigour: use a formal register, " +
		"precise domain vocabulary, and a clear argument structure. " +
		"Favour complex sentences where they add clarity, not obscurity. " +
		"Return only the revised text — no preamble, no explanations.",

	"journalist": "You are a seasoned copy editor at a national newspaper. " +
		"Revise the draft for maximum clarity and reader impact: " +
		"active voice, tight sentences, a strong opening that earns the reader's attention. " +
		"Cut jargon; keep every word accountable. " +
		"Return only the revised text — no preamble, no explanations.",

	"engineer": "You are a technical writing editor. " +
		"Revise the draft for precision and usability: " +
		"concrete examples over abstractions, consistent terminology, " +
		"structured prose that scans well (short paragraphs, lists where helpful). " +
		"Define any term that a competent engineer outside the domain might not know. " +
		"Return only the revised text — no preamble, no explanations.",
}

// defaultMode is used when the client sends an unrecognised or empty mode.
const defaultMode = "academic"

// resolveSystemPrompt returns the system prompt for the requested mode,
// falling back to the default if the mode is unrecognised.
func resolveSystemPrompt(mode string) (string, string) {
	if p, ok := systemPrompts[mode]; ok {
		return mode, p
	}
	return defaultMode, systemPrompts[defaultMode]
}

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
	Mode   string `json:"mode"` // "academic" | "journalist" | "engineer"
}

// DonePayload is the body of the terminal `done` SSE event. It carries the
// metadata the client needs to finalise the streamed turn — the assigned
// revision id, the resolved mode, and the turn number.
type DonePayload struct {
	RevisionID int    `json:"revision_id"`
	Mode       string `json:"mode"`
	Turn       int    `json:"turn"`
}

// Revise streams a new revision turn as a sequence of Server-Sent Events.
// The wire format is three event kinds:
//
//	event: delta   data: {"text":"…"}      // one per text chunk from Claude
//	event: done    data: {"revision_id":…} // terminal, after persistence
//	event: error   data: {"error":"…"}     // terminal, on failure
//
// Validation and history loading happen before any SSE headers are written,
// so genuine 4xx/5xx responses can still be returned as plain JSON. Once the
// stream begins, all errors flow as `error` events instead.
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
		if req.Prompt == "" {
			http.Error(w, "prompt is required", http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported by transport", http.StatusInternalServerError)
			return
		}

		mode, sysPrompt := resolveSystemPrompt(req.Mode)

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

		history, err := orm.FetchMany(conn, domain.ListRevisionsForDraft(id))
		if err != nil {
			log.Error().Err(err).Send()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// From this point on we commit to SSE. Switch the response into
		// streaming mode and treat any further error as an `error` event.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		messages := buildMessages(draft.Content, history, req.Prompt)
		stream := ai.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeHaiku4_5,
			MaxTokens: 1024,
			System:    []anthropic.TextBlockParam{{Text: sysPrompt}},
			Messages:  messages,
		})

		// `accumulator` reassembles the full Message from the event stream so
		// we can persist the final completion + usage at the end. The SDK
		// ships this helper because it's the same logic every consumer needs.
		accumulator := anthropic.Message{}
		for stream.Next() {
			event := stream.Current()
			if err := accumulator.Accumulate(event); err != nil {
				writeSSEError(w, flusher, errors.Wrap(err, "accumulate"))
				return
			}

			// Forward only the deltas the client cares about — the text
			// chunks. Other events (start, content_block_start, message_delta,
			// stop) carry metadata we use server-side via the accumulator.
			if cb, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if td, ok := cb.Delta.AsAny().(anthropic.TextDelta); ok {
					writeSSEEvent(w, flusher, "delta", map[string]string{"text": td.Text})
				}
			}
		}
		if err := stream.Err(); err != nil {
			writeSSEError(w, flusher, errors.Wrap(err, "anthropic stream"))
			return
		}
		if len(accumulator.Content) == 0 {
			writeSSEError(w, flusher, errors.New("empty completion from model"))
			return
		}

		// Persist the assembled completion as a single revision row. Storage
		// remains atomic: from the database's perspective there's still one
		// insert per turn, regardless of how the wire transported it.
		completion := accumulator.Content[0].Text
		rows, err := orm.Exec(conn, domain.InsertRevision(&domain.Revision{
			DraftID:    id,
			Prompt:     req.Prompt,
			Completion: completion,
			Mode:       mode,
		}))
		if err != nil {
			writeSSEError(w, flusher, errors.Wrap(err, "save revision"))
			return
		}

		turn := len(history) + 1
		log.Info().
			Int("draft_id", id).
			Int("revision_id", rows[0].ID).
			Int("turn", turn).
			Str("mode", mode).
			Int("tokens_in", int(accumulator.Usage.InputTokens)).
			Int("tokens_out", int(accumulator.Usage.OutputTokens)).
			Msg("revision saved (stream)")

		writeSSEEvent(w, flusher, "done", DonePayload{
			RevisionID: rows[0].ID,
			Mode:       mode,
			Turn:       turn,
		})
	}
}

// writeSSEEvent emits a single named event with a JSON-encoded data body and
// flushes immediately so the chunk hits the wire without sitting in a buffer.
//
// SSE framing is plain text: an optional `event:` line, one or more `data:`
// lines, then a blank line that delimits the event from the next.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, name string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshalling our own response objects shouldn't fail, but if it does
		// there's no useful frame to write — just skip and let the client
		// notice the connection ends without a `done`.
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body)
	flusher.Flush()
}

// writeSSEError emits a terminal `error` event. Once this is sent the handler
// must return — by convention there's nothing useful after it.
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, err error) {
	writeSSEEvent(w, flusher, "error", map[string]string{"error": err.Error()})
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
