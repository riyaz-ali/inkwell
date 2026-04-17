// Package schema loads and applies SQL schema migrations from embedded files.
//
// Each migration is a v<N>.sql file in this directory. Files are discovered at
// build time via embed.FS, sorted by version, and applied via tools.sql/schema
// which tracks applied versions through SQLite's PRAGMA user_version.
package schema

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"github.com/pkg/errors"
	tschema "github.com/riyaz-ali/tools.sql/schema"
)

//go:embed *.sql
var migrations embed.FS

var versionPattern = regexp.MustCompile(`^v(\d+)\.sql$`)

// sqlMigration adapts an embedded SQL file to the tools.sql/schema.Migration interface.
type sqlMigration struct {
	name    string
	version int
	sql     string
}

func (m sqlMigration) Name() string                  { return m.name }
func (m sqlMigration) Version() int                  { return m.version }
func (m sqlMigration) Apply(c *sqlite.Conn) error    { return sqlitex.ExecScript(c, m.sql) }

// Apply discovers all v<N>.sql migrations embedded in this package and applies
// any not yet recorded against the connection's user_version.
func Apply(conn *sqlite.Conn) error {
	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return errors.Wrap(err, "read embedded migrations")
	}

	var ms []tschema.Migration
	for _, e := range entries {
		m := versionPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		version, err := strconv.Atoi(m[1])
		if err != nil {
			return errors.Wrapf(err, "parse version from %q", e.Name())
		}

		data, err := migrations.ReadFile(e.Name())
		if err != nil {
			return errors.Wrapf(err, "read %q", e.Name())
		}

		ms = append(ms, sqlMigration{
			name:    fmt.Sprintf("v%d", version),
			version: version,
			sql:     string(data),
		})
	}

	sort.Slice(ms, func(i, j int) bool { return ms[i].Version() < ms[j].Version() })

	return tschema.Apply(conn, ms)
}
