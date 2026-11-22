package schema

import (
	"context"
	"fmt"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
)

// ignoredTables are bookkeeping tables of migration tools.
var ignoredTables = map[string]bool{"goose_db_version": true, "schema_migrations": true}

// Introspect reads the tables of the current schema of the connected database.
func Introspect(ctx context.Context, q postgres.Querier) (*Schema, error) {
	s := newSchema()

	rows, err := q.Query(ctx, `
SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
       COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), a.attidentity <> ''
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`)
	if err != nil {
		return nil, postgres.MapError(err)
	}
	for rows.Next() {
		var table string
		c := &Column{}
		if err := rows.Scan(&table, &c.Name, &c.Type, &c.NotNull, &c.Default, &c.Identity); err != nil {
			rows.Close()
			return nil, postgres.MapError(err)
		}
		if ignoredTables[table] {
			continue
		}
		t := s.Tables[table]
		if t == nil {
			t = &Table{Name: table}
			s.Tables[table] = t
		}
		if c.Identity {
			c.Default = "" // the sequence default is an implementation detail of the identity
		}
		c.Type = NormalizeType(c.Type)
		t.Columns = append(t.Columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, postgres.MapError(err)
	}

	rows, err = q.Query(ctx, `
SELECT c.relname, con.conname, con.contype::text,
  (SELECT array_agg(a.attname ORDER BY k.ord) FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
     JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum),
  COALESCE(rc.relname, ''),
  COALESCE((SELECT array_agg(a.attname ORDER BY k.ord) FROM unnest(con.confkey) WITH ORDINALITY k(attnum, ord)
     JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum), '{}'),
  con.confdeltype::text,
  CASE WHEN con.contype = 'c' THEN pg_get_constraintdef(con.oid) ELSE '' END
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_class rc ON rc.oid = con.confrelid
WHERE n.nspname = current_schema() AND con.contype IN ('p', 'u', 'f', 'c')
ORDER BY c.relname, con.conname`)
	if err != nil {
		return nil, postgres.MapError(err)
	}
	for rows.Next() {
		var table, name, kind, refTable, onDel, def string
		var cols, refCols []string
		if err := rows.Scan(&table, &name, &kind, &cols, &refTable, &refCols, &onDel, &def); err != nil {
			rows.Close()
			return nil, postgres.MapError(err)
		}
		t := s.Tables[table]
		if t == nil {
			continue
		}
		switch kind {
		case "p":
			t.PK = cols
		case "u":
			t.Uniques = append(t.Uniques, &Unique{Name: name, Columns: cols, Constraint: true})
		case "c":
			t.Checks = append(t.Checks, &Check{Name: name, Def: def})
		case "f":
			t.FKs = append(t.FKs, &ForeignKey{Name: name, Columns: cols, RefTable: refTable, RefColumns: refCols,
				OnDelete: map[string]string{"c": "cascade", "r": "restrict", "n": "set null"}[onDel]})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, postgres.MapError(err)
	}

	// Indexes that are not backed by a constraint and are plain column lists.
	rows, err = q.Query(ctx, `
SELECT c.relname, i.relname, ix.indisunique,
  (SELECT array_agg(a.attname ORDER BY k.ord) FROM unnest(ix.indkey::int2[]) WITH ORDINALITY k(attnum, ord)
     JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum)
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class c ON c.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND NOT ix.indisprimary AND ix.indexprs IS NULL AND ix.indpred IS NULL
  AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = ix.indexrelid)
ORDER BY c.relname, i.relname`)
	if err != nil {
		return nil, postgres.MapError(err)
	}
	for rows.Next() {
		var table, name string
		var unique bool
		var cols []string
		if err := rows.Scan(&table, &name, &unique, &cols); err != nil {
			rows.Close()
			return nil, postgres.MapError(err)
		}
		t := s.Tables[table]
		if t == nil {
			continue
		}
		if unique {
			t.Uniques = append(t.Uniques, &Unique{Name: name, Columns: cols})
		} else {
			t.Indexes = append(t.Indexes, &Index{Name: name, Columns: cols})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, postgres.MapError(err)
	}
	if len(s.Tables) == 0 {
		return s, nil
	}
	for name, t := range s.Tables {
		if len(t.Columns) == 0 {
			return nil, fmt.Errorf("schema: table %q has no columns", name)
		}
	}
	return s, nil
}
