package domain

import (
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex/orm"
)

// Revision is a single turn in a draft's revision history: the user's prompt
// and the assistant's reply. Replaying all revisions of a draft in order
// reconstructs the full conversation sent to the model on the next turn.
type Revision struct {
	ID         int    `db:"id"`
	DraftID    int    `db:"draft_id"`
	Prompt     string `db:"prompt"`
	Completion string `db:"completion"`
}

// InsertRevision appends a new revision to a draft's history.
func InsertRevision(r *Revision) orm.I[Revision, *Revision] {
	return orm.I[Revision, *Revision]{
		QueryStr: `INSERT INTO revisions (draft_id, prompt, completion)
		           VALUES (?, ?, ?)
		           RETURNING id, draft_id, prompt, completion`,
		ArgSet: []*Revision{r},
		Bind: func(stmt *sqlite.Stmt, r *Revision) error {
			stmt.BindInt64(1, int64(r.DraftID))
			stmt.BindText(2, r.Prompt)
			stmt.BindText(3, r.Completion)
			return nil
		},
		Val: func(stmt *sqlite.Stmt) (*Revision, error) {
			return orm.ScanAs[Revision](stmt)
		},
	}
}

// ListRevisionsForDraft returns every revision attached to a draft, in the
// order they were created. This ordering is what the handler replays as
// alternating user/assistant messages when building the Claude request.
func ListRevisionsForDraft(draftID int) orm.Q[Revision] {
	return orm.Q[Revision]{
		QueryStr: `SELECT id, draft_id, prompt, completion
		           FROM revisions
		           WHERE draft_id = ?
		           ORDER BY id ASC`,
		Bind: func(stmt *sqlite.Stmt) error {
			stmt.BindInt64(1, int64(draftID))
			return nil
		},
		Val: func(stmt *sqlite.Stmt) (*Revision, error) {
			return orm.ScanAs[Revision](stmt)
		},
	}
}
