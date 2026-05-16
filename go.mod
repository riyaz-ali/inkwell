module github.com/riyaz-ali/inkwell

go 1.23.0

require (
	crawshaw.io/sqlite v0.3.2
	github.com/anthropics/anthropic-sdk-go v1.37.0
	github.com/go-chi/chi/v5 v5.2.5
	github.com/pkg/errors v0.9.1
	github.com/rs/zerolog v1.35.0
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
)

replace crawshaw.io/sqlite v0.3.2 => github.com/riyaz-ali/sqlite3 v0.0.0-20260516143954-99e6c1ebd044
