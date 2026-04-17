// Package completion implements the /api/complete HTTP endpoint — the core
// single-turn "improve this draft" flow introduced in article 1.
package completion

import (
	"context"

	"crawshaw.io/sqlite/sqlitex"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/pkg/errors"
	tools "github.com/riyaz-ali/tools.sql"
	"github.com/rs/zerolog"

	"github.com/riyaz-ali/inkwell/internal/domain"
	"github.com/riyaz-ali/inkwell/internal/util"
)

const systemPrompt = "You are a writing assistant. Help the user improve their draft. Return only the revised text — no preamble, no explanations."

// Request is the JSON body accepted by POST /api/complete.
type Request struct {
	Content string `json:"content"`
	Prompt  string `json:"prompt"`
}

// Response is the JSON body returned by POST /api/complete.
type Response struct {
	Completion string `json:"completion"`
	DraftID    int    `json:"draft_id"`
}

// Complete returns a handler that sends the draft + prompt to Claude, saves
// the revised text as a new draft, and responds with it.
func Complete(pool *sqlitex.Pool, ai *anthropic.Client) util.HandlerFunc[Request, Response] {
	return func(ctx context.Context, req Request) (*Response, error) {
		log := zerolog.Ctx(ctx)

		if req.Content == "" || req.Prompt == "" {
			return nil, errors.New("content and prompt are required")
		}

		msg, err := ai.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeHaiku4_5,
			MaxTokens: 1024,
			System: []anthropic.TextBlockParam{
				{Text: systemPrompt},
			},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock(req.Content + "\n\n" + req.Prompt)),
			},
		})
		if err != nil {
			return nil, errors.Wrap(err, "anthropic completion")
		}
		if len(msg.Content) == 0 {
			return nil, errors.New("empty completion from model")
		}

		completion := msg.Content[0].Text

		conn := pool.Get(ctx)
		defer pool.Put(conn)

		rows, err := tools.Exec(conn, domain.InsertDraft(completion))
		if err != nil {
			return nil, errors.Wrap(err, "save draft")
		}

		log.Info().Int("draft_id", rows[0].ID).Int("tokens_out", int(msg.Usage.OutputTokens)).Msg("completion saved")

		return &Response{
			Completion: completion,
			DraftID:    rows[0].ID,
		}, nil
	}
}
