// Package schema loads and applies SQL schema migrations from embedded files.
//
// Each migration is a v<N>.sql file in this directory. Files are discovered at
// build time via embed.FS, sorted by version, and applied via crawshaw.io/sqlite/sqlitex/schema
package schema

import (
	"embed"

	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex/schema"
)

//go:embed *.sql
var migrations embed.FS

// Apply discovers all v<N>.sql migrations embedded in this package and applies
// any not yet recorded against the connection's user_version.
func Apply(conn *sqlite.Conn) error {
	return schema.Apply(conn, schema.ReadFrom(migrations))
}
