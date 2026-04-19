// Package domain defines the application's domain objects and their database
// queries. Each entity lives in its own file alongside the queries that
// operate on it.
package domain

import (
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex/orm"
)

// Draft is a single saved piece of text — the output of a completion request,
// or (in later articles) a user-authored snapshot of a document.
type Draft struct {
	ID      int    `db:"id"`
	Content string `db:"content"`
}

// InsertDraft inserts a new row into drafts and returns the stored value.
func InsertDraft(content string) orm.I[Draft, string] {
	return orm.I[Draft, string]{
		QueryStr: `INSERT INTO drafts (content) VALUES (?) RETURNING id, content`,
		ArgSet:   []string{content},
		Bind: func(stmt *sqlite.Stmt, s string) error {
			stmt.BindText(1, s)
			return nil
		},
		Val: func(stmt *sqlite.Stmt) (*Draft, error) {
			return orm.ScanAs[Draft](stmt)
		},
	}
}

// GetDraftByID fetches a single draft by its primary key, or nil if not found.
func GetDraftByID(id int64) orm.Q[Draft] {
	return orm.Q[Draft]{
		QueryStr: `SELECT id, content FROM drafts WHERE id = ?`,
		Bind: func(stmt *sqlite.Stmt) error {
			stmt.BindInt64(1, id)
			return nil
		},
		Val: func(stmt *sqlite.Stmt) (*Draft, error) {
			return orm.ScanAs[Draft](stmt)
		},
	}
}
