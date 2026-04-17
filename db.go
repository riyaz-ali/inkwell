package main

import (
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	schema "github.com/riyaz-ali/tools.sql/schema"
)

type initMigration struct{}

func (initMigration) Name() string { return "init" }
func (initMigration) Version() int { return 1 }
func (initMigration) Apply(conn *sqlite.Conn) error {
	return sqlitex.ExecScript(conn, `
		CREATE TABLE IF NOT EXISTS drafts (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			content    TEXT    NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- populated from article 3 onward (multi-turn revision history)
		CREATE TABLE IF NOT EXISTS revisions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			draft_id   INTEGER NOT NULL REFERENCES drafts(id),
			prompt     TEXT    NOT NULL,
			completion TEXT    NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
}

func openDB(path string) (*sqlite.Conn, error) {
	conn, err := sqlite.OpenConn(path, 0)
	if err != nil {
		return nil, err
	}

	if err := sqlitex.Exec(conn, "PRAGMA journal_mode=WAL", nil); err != nil {
		conn.Close()
		return nil, err
	}

	if err := schema.Apply(conn, []schema.Migration{initMigration{}}); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}
