# AGENT.md

Guidance for AI coding agents working on this repository.

## What this repo is

Inkwell is the companion repo for the **"Building with AI"** blog series on
[riyazali.net](https://riyazali.net). Each article in the series introduces one
Anthropic Claude API concept and lands a matching feature here. The repo and
the series evolve together — readers clone at each article and get a runnable
snapshot of the concept being taught.

**Implication:** article ordering drives feature ordering. Don't land features
ahead of the article that introduces them, even if it would be architecturally
cleaner. Stubs are fine (e.g. the `revisions` table exists in the schema from
article 1 but isn't used until article 3).

## Article → feature roadmap

| # | Article                          | Feature                                        |
|---|----------------------------------|------------------------------------------------|
| 1 | Series intro + vision            | Repo scaffold, editor skeleton, basic /complete|
| 2 | Anatomy of an API call           | /v1/messages request/response breakdown        |
| 3 | Multi-turn conversations         | Threaded revision history                      |
| 4 | System prompts & roles           | Writing modes (Academic / Journalist / Engineer)|
| 5 | Streaming responses              | Live typewriter output via SSE                 |
| 6 | Structured output                | "Analyse draft" → JSON critique                |
| 7 | Tool use (function calling)      | `save_draft()`, `get_revision()` tools         |
| 8 | Agentic loop                     | Auto-polish until quality threshold met        |
| 9 | Prompt caching                   | Large doc context cached across edits          |
|10 | Latency & instrumentation        | TTFT + total latency dashboard                 |
|11 | Failure handling                 | Rate limits, retries, backoff, error UX        |

## Tech stack (non-negotiable)

- **Backend:** Go
- **SQLite driver:** [`crawshaw.io/sqlite`](https://github.com/crawshaw/sqlite)
  (CGO-based, **not** `database/sql` + `modernc.org/sqlite`)
- **SQL wrapper:** [`github.com/riyaz-ali/tools.sql`](https://github.com/riyaz-ali/tools.sql) —
  the author's own typed wrapper. Use this for all queries and migrations.
- **Anthropic SDK:** [`github.com/anthropics/anthropic-sdk-go`](https://github.com/anthropics/anthropic-sdk-go)
- **Frontend:** [oat.js](https://oat.ink) from CDN, vanilla JS, no build step
- **Infra:** single `docker-compose.yml`

### tools.sql cheat sheet

```go
// SELECT
var q = tools.Q[Draft]{
    QueryStr: `SELECT id, content FROM drafts WHERE id = ?`,
    Bind:     func(s *sqlite.Stmt) error { s.BindInt64(1, id); return nil },
    Val:      func(s *sqlite.Stmt) (*Draft, error) { return tools.ScanAs[Draft](s) },
}
draft, err := tools.FetchOne(conn, q)

// INSERT with RETURNING
func draftInsert(content string) tools.I[Draft, string] {
    return tools.I[Draft, string]{
        QueryStr: `INSERT INTO drafts (content) VALUES (?) RETURNING id, content`,
        ArgSet:   []string{content},
        Bind:     func(s *sqlite.Stmt, v string) error { s.BindText(1, v); return nil },
        Val:      func(s *sqlite.Stmt) (*Draft, error) { return tools.ScanAs[Draft](s) },
    }
}
rows, err := tools.Exec(conn, draftInsert(text))

// Migrations
schema.Apply(conn, []schema.Migration{initMigration{}, /* ... */})
```

Struct fields use `db:"col_name"` tags for `ScanAs` to map correctly.

## Project structure

```
inkwell/
├── main.go               # HTTP server + handlers
├── db.go                 # openDB + migrations
├── web/
│   ├── index.html        # oat.js UI
│   └── app.js            # vanilla JS
├── Dockerfile
├── docker-compose.yml
├── .env.example
└── README.md
```

Flat by design — article 1 readers see the whole project at a glance. Refactor
into packages only when a specific article motivates it.

## Running locally

```bash
cp .env.example .env   # set ANTHROPIC_API_KEY
go run .               # http://localhost:8080
```

```bash
docker compose up      # builds + runs with volume-persisted DB at ./data/
```

## Writing style (if asked to draft article prose)

- Conversational but precise, no fluff
- Opens with **why** before **what/how**
- Em-dashes and parenthetical asides are natural, not to be avoided
- Prose for narrative; bullets only for genuine lists
- Each article teases the next one at the end
- Code examples should be runnable as-is at the corresponding `git checkout`

## When in doubt

- Don't add features, refactor, or "improve" code beyond the scope of the
  current article
- Don't switch libraries (especially the SQLite stack) without explicit ask
- Don't introduce build tooling on the frontend
- Ask before renaming or restructuring existing files — the blog posts may
  reference them by path
