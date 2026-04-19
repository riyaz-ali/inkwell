# AGENT.md

Guidance for AI coding agents working on this repository.

## What this repo is

Inkwell is the companion repo for the **"Building with AI"** blog series on
[riyazali.net](https://riyazali.net). Each article introduces one Anthropic
Claude API concept and lands a matching feature here. The repo and the series
evolve together — readers clone at each article and get a runnable snapshot
of the concept being taught.

**Implication:** article ordering drives feature ordering. Don't land features
ahead of the article that introduces them. Stubs are fine (the `revisions`
table exists in schema v1 but isn't used until article 3).

## Article → feature roadmap

| # | Article                          | Feature                                         |
|---|----------------------------------|-------------------------------------------------|
| 1 | Series intro + vision            | Repo scaffold, editor skeleton, basic /complete |
| 2 | Anatomy of an API call           | /v1/messages request/response breakdown         |
| 3 | Multi-turn conversations         | Threaded revision history                       |
| 4 | System prompts & roles           | Writing modes (Academic / Journalist / Engineer)|
| 5 | Streaming responses              | Live typewriter output via SSE                  |
| 6 | Structured output                | "Analyse draft" → JSON critique                 |
| 7 | Tool use (function calling)      | `save_draft()`, `get_revision()` tools          |
| 8 | Agentic loop                     | Auto-polish until quality threshold met         |
| 9 | Prompt caching                   | Large doc context cached across edits           |
|10 | Latency & instrumentation        | TTFT + total latency dashboard                  |
|11 | Failure handling                 | Rate limits, retries, backoff, error UX         |

## Project structure

```
inkwell/
├── inkwell.go                       # entry point: config → pool → router → serve
├── internal/
│   ├── config/config.go             # Config struct + Load() from env
│   ├── schema/
│   │   ├── schema.go                # embed.FS loader + Apply(conn)
│   │   └── v<N>.sql                 # one file per version (v1.sql, v2.sql, …)
│   ├── domain/
│   │   ├── draft.go                 # Draft struct + colocated tools.sql queries
│   │   └── revision.go              # Revision struct + colocated queries (article 3)
│   ├── drafts/
│   │   └── drafts.go                # /api/drafts + /api/drafts/{id}/revisions
│   └── util/
│       └── http.go                  # generic HandlerFunc[I,O]
└── web/                             # static frontend (oat.js + vanilla JS)
```

Layout mirrors [riyaz-ali/wirefire](https://github.com/riyaz-ali/wirefire) —
domain objects colocate their SQL with their struct; each HTTP feature is its
own package; `internal/util` holds the generic handler wrapper.

## Tech stack (non-negotiable)

- **Backend:** Go 1.23+
- **Router:** [`chi/v5`](https://github.com/go-chi/chi)
- **Logging:** [`zerolog`](https://github.com/rs/zerolog) — attached to
  context in the entry point, accessed via `zerolog.Ctx(ctx)` inside handlers.
  Never take a `*zerolog.Logger` as a function parameter.
- **Errors:** [`pkg/errors`](https://github.com/pkg/errors) — wrap with
  `.Wrap(err, "context")`, don't use `fmt.Errorf("%w", err)`
- **SQLite driver:** [`crawshaw.io/sqlite`](https://github.com/crawshaw/sqlite)
  (CGO-based; **not** `database/sql` or `modernc.org/sqlite`)
- **SQL wrapper:** [`github.com/riyaz-ali/tools.sql`](https://github.com/riyaz-ali/tools.sql) —
  the author's own typed generics wrapper
- **Anthropic SDK:** [`github.com/anthropics/anthropic-sdk-go`](https://github.com/anthropics/anthropic-sdk-go)
- **Frontend:** [oat.js](https://oat.ink) from CDN, vanilla JS, no build step
- **Infra:** single `docker-compose.yml`

## Pattern: adding a new HTTP handler

Each feature is a package under `internal/`. The handler is a function that
takes its dependencies and returns a `util.HandlerFunc[Request, Response]`.
Dependencies are injected via the constructor's arguments — **never via
package-level globals**.

```go
// internal/critique/critique.go
package critique

import (
    "context"
    "crawshaw.io/sqlite/sqlitex"
    "github.com/riyaz-ali/inkwell/internal/util"
)

type Request  struct { DraftID int `json:"draft_id"` }
type Response struct { Tone    string `json:"tone"` }

func Analyse(pool *sqlitex.Pool, ai *anthropic.Client) util.HandlerFunc[Request, Response] {
    return func(ctx context.Context, req Request) (*Response, error) {
        log := zerolog.Ctx(ctx)
        conn := pool.Get(ctx); defer pool.Put(conn)
        // ... business logic ...
        return &Response{Tone: "formal"}, nil
    }
}
```

Mount in `inkwell.go`:

```go
r.Method(http.MethodPost, "/api/analyse", critique.Analyse(pool, &ai))
```

The generic `util.HandlerFunc`:
- Decodes the request body as JSON into `I`
- Invokes your function with the request `Context` (carries logger + cancellation)
- Encodes the `*O` return value as JSON
- Logs + surfaces handler errors as 400; encode/decode errors as 500

For endpoints that aren't JSON-in/JSON-out (file serving, SSE, redirects),
use a plain `http.HandlerFunc` returned from a constructor — see
`inkwell.go`'s static file mount, or wirefire's streaming handlers for a
precedent.

## Pattern: adding a new domain object

Put the struct and all its queries in one file under `internal/domain/`.

```go
// internal/domain/revision.go
package domain

import (
    "crawshaw.io/sqlite"
    tools "github.com/riyaz-ali/tools.sql"
)

type Revision struct {
    ID         int    `db:"id"`
    DraftID    int    `db:"draft_id"`
    Prompt     string `db:"prompt"`
    Completion string `db:"completion"`
}

func InsertRevision(r *Revision) tools.I[Revision, *Revision] {
    return tools.I[Revision, *Revision]{
        QueryStr: `INSERT INTO revisions (draft_id, prompt, completion)
                   VALUES (?, ?, ?) RETURNING id, draft_id, prompt, completion`,
        ArgSet:   []*Revision{r},
        Bind: func(stmt *sqlite.Stmt, r *Revision) error {
            stmt.BindInt64(1, int64(r.DraftID))
            stmt.BindText(2, r.Prompt)
            stmt.BindText(3, r.Completion)
            return nil
        },
        Val: func(stmt *sqlite.Stmt) (*Revision, error) {
            return tools.ScanAs[Revision](stmt)
        },
    }
}

func ListRevisionsForDraft(draftID int) tools.Q[Revision] {
    return tools.Q[Revision]{
        QueryStr: `SELECT id, draft_id, prompt, completion
                   FROM revisions WHERE draft_id = ? ORDER BY id ASC`,
        Bind: func(stmt *sqlite.Stmt) error { stmt.BindInt64(1, int64(draftID)); return nil },
        Val:  func(stmt *sqlite.Stmt) (*Revision, error) { return tools.ScanAs[Revision](stmt) },
    }
}
```

Conventions:
- `db:"col_name"` tags drive `tools.ScanAs` mapping
- `db:"col,json"` marshals/unmarshals the field as JSON (for nested structs)
- Query functions take concrete arguments and return `tools.Q[T]` or `tools.I[M, A]`
- Callers invoke `tools.FetchOne(conn, q)`, `tools.FetchMany(conn, q)`,
  or `tools.Exec(conn, q)`

## Pattern: adding a schema migration

Add a new `internal/schema/v<N>.sql` file with version `N` greater than any
existing. The embed loader picks it up automatically; `schema.Apply` runs
any migration whose version is above `PRAGMA user_version`.

## Running locally

```bash
cp .env.example .env   # set ANTHROPIC_API_KEY
go run .               # http://localhost:8080
```

```bash
docker compose up      # volume-persisted DB at ./data/
```

## Writing style (if asked to draft article prose)

- Conversational but precise, no fluff
- Opens with **why** before **what/how**
- Em-dashes and parenthetical asides are natural, not to be avoided
- Prose for narrative; bullets only for genuine lists
- Each article teases the next one at the end

## When in doubt

- Don't add features, refactor, or "improve" code beyond the scope of the
  current article
- Don't switch libraries (SQLite stack, router, logger) without explicit ask
- Don't introduce build tooling on the frontend
- Don't take loggers as function arguments — fetch them from context
- Ask before renaming or restructuring existing files — the blog posts may
  reference them by path
