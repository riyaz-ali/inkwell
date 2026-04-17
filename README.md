# Inkwell

A writing intelligence platform built incrementally as a companion to the
[Building with AI](https://riyazali.net/series/building-with-ai) blog series

## Quick start

```bash
git clone https://github.com/riyaz-ali/inkwell
cd inkwell

cp .env.example .env
# edit .env and set ANTHROPIC_API_KEY

go run .
# open http://localhost:8080
```

## Stack

- **Backend** — Go, [crawshaw.io/sqlite](https://github.com/crawshaw/sqlite),
  [tools.sql](https://github.com/riyaz-ali/tools.sql)
- **Frontend** — [oat.js](https://oat.ink)
- **AI** — [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go)
