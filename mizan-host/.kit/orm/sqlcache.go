package orm

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// sqlCache holds every statement that depends only on the model, built once. Statements whose shape
// depends on the data (which defaults to use, which columns to update) are built on first use for each
// shape and then reused.
type sqlCache struct {
	selectList      string // "id", "email", ... of every field
	qualifiedSelect string // "table"."id", "table"."email", ...
	live            string // "deleted_at" IS NULL, or ""

	defaultable []*Field // fields the database can default, in field order
	insertable  []*Field // fields written on insert
	updatable   []*Field // fields written by a full update

	getByPK        string
	deleteByPK     string
	softDeleteByPK string
	pkWhere        string

	inserts sync.Map // uint64 -> *insertPlan
	updates sync.Map // string -> *updatePlan
	bulk    sync.Map // struct{} -> *bulkPlan
}

func (c *sqlCache) init(m *Model) {
	names := make([]string, len(m.Fields))
	for i, f := range m.Fields {
		names[i] = f.quoted
	}
	c.selectList = strings.Join(names, ", ")
	c.qualifiedSelect = qualifiedList(m, m.Fields)
	if m.SoftDelete != nil {
		c.live = m.SoftDelete.quoted + " IS NULL"
	}

	for _, f := range m.Fields {
		if f.hasDefault() {
			c.defaultable = append(c.defaultable, f)
		}
		if !f.Auto && !f.ReadOnly {
			c.insertable = append(c.insertable, f)
			if !f.PK && !f.CreatedAt && !f.SoftDelete && !f.UpdatedAt && !f.Version {
				c.updatable = append(c.updatable, f)
			}
		}
	}

	conds := make([]string, len(m.PK))
	for i, f := range m.PK {
		conds[i] = f.quoted + " = $" + strconv.Itoa(i+1)
	}
	c.pkWhere = strings.Join(conds, " AND ")
	where := c.pkWhere
	if c.live != "" {
		where += " AND " + c.live
	}
	c.getByPK = "SELECT " + c.selectList + " FROM " + m.quotedTable + " WHERE " + where
	c.deleteByPK = "DELETE FROM " + m.quotedTable + " WHERE " + c.pkWhere
	if m.SoftDelete != nil {
		c.softDeleteByPK = "UPDATE " + m.quotedTable + " SET " + m.SoftDelete.quoted + " = now() WHERE " + where
	}
}

// insertPlan is one INSERT shape: which columns take a bind argument and which use DEFAULT.
type insertPlan struct {
	sql       string
	args      []*Field // fields bound as $1..$n, in order
	returning []*Field // columns the database generated, read back into the struct
}

// insertMask has one bit per defaultable field that is zero in the value being inserted.
func (m *Model) insertMask(rv reflect.Value) uint64 {
	var mask uint64
	for i, f := range m.cache.defaultable {
		if rv.FieldByIndex(f.Index).IsZero() {
			mask |= 1 << i
		}
	}
	return mask
}

func (m *Model) insertPlan(mask uint64) *insertPlan {
	if p, ok := m.cache.inserts.Load(mask); ok {
		return p.(*insertPlan)
	}
	c := &m.cache
	useDefault := map[*Field]bool{}
	for i, f := range c.defaultable {
		if mask&(1<<i) != 0 {
			useDefault[f] = true
		}
	}
	p := &insertPlan{}
	var cols, vals []string
	for _, f := range c.insertable {
		cols = append(cols, f.quoted)
		if useDefault[f] {
			vals = append(vals, "DEFAULT")
			continue
		}
		p.args = append(p.args, f)
		vals = append(vals, "$"+strconv.Itoa(len(p.args)))
	}
	for _, f := range m.Fields {
		if f.Auto || useDefault[f] || f.Version {
			p.returning = append(p.returning, f)
		}
	}
	var b strings.Builder
	b.WriteString("INSERT INTO " + m.quotedTable)
	if len(cols) == 0 {
		b.WriteString(" DEFAULT VALUES")
	} else {
		b.WriteString(" (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")")
	}
	writeReturning(&b, p.returning)
	p.sql = b.String()
	actual, _ := c.inserts.LoadOrStore(mask, p)
	return actual.(*insertPlan)
}

func writeReturning(b *strings.Builder, fields []*Field) {
	if len(fields) == 0 {
		return
	}
	b.WriteString(" RETURNING ")
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.quoted)
	}
}

// updatePlan is one UPDATE-by-primary-key shape.
type updatePlan struct {
	sql       string
	set       []*Field // bound first
	returning []*Field
}

func (m *Model) updatePlan(set []*Field) *updatePlan {
	var key strings.Builder
	for _, f := range set {
		key.WriteString(f.Column)
		key.WriteByte(',')
	}
	if p, ok := m.cache.updates.Load(key.String()); ok {
		return p.(*updatePlan)
	}
	p := &updatePlan{set: set}
	var b strings.Builder
	b.WriteString("UPDATE " + m.quotedTable + " SET ")
	n := 0
	explicit := map[*Field]bool{}
	for _, f := range set {
		explicit[f] = true
		if n > 0 {
			b.WriteString(", ")
		}
		n++
		b.WriteString(f.quoted + " = $" + strconv.Itoa(n))
	}
	if m.UpdatedAt != nil && !explicit[m.UpdatedAt] {
		if n > 0 {
			b.WriteString(", ")
		}
		b.WriteString(m.UpdatedAt.quoted + " = now()")
		p.returning = append(p.returning, m.UpdatedAt)
		n++
	}
	if m.Version != nil {
		if n > 0 {
			b.WriteString(", ")
		}
		b.WriteString(m.Version.quoted + " = " + m.Version.quoted + " + 1")
		p.returning = append(p.returning, m.Version)
	}
	// WHERE pk = $k+1.. [AND version = $..] [AND live]
	arg := len(set)
	b.WriteString(" WHERE ")
	for i, f := range m.PK {
		if i > 0 {
			b.WriteString(" AND ")
		}
		arg++
		b.WriteString(f.quoted + " = $" + strconv.Itoa(arg))
	}
	if m.Version != nil {
		arg++
		b.WriteString(" AND " + m.Version.quoted + " = $" + strconv.Itoa(arg))
	}
	if m.cache.live != "" {
		b.WriteString(" AND " + m.cache.live)
	}
	writeReturning(&b, p.returning)
	p.sql = b.String()
	actual, _ := m.cache.updates.LoadOrStore(key.String(), p)
	return actual.(*updatePlan)
}

// hasArrayColumn reports whether a column is an SQL array: unnest() would flatten arrays of arrays, so
// such models cannot use the single-statement bulk insert.
func (m *Model) hasArrayColumn() bool {
	for _, f := range m.cache.insertable {
		if strings.HasSuffix(f.SQLType, "[]") {
			return true
		}
	}
	return false
}

// bulkInsert builds the single-statement bulk INSERT over unnest(): one round trip and one parse for any
// number of rows. Columns the database can default receive NULL for zero values and fall back to the
// default expression through COALESCE.
func (m *Model) bulkInsertSQL() (string, []*Field, []*Field) {
	if v, ok := m.cache.bulk.Load(struct{}{}); ok {
		b := v.(*bulkPlan)
		return b.sql, b.cols, b.returning
	}
	c := &m.cache
	var cols, sel, arrays, names []string
	var returning []*Field
	for i, f := range c.insertable {
		cols = append(cols, f.quoted)
		names = append(names, f.quoted)
		arrays = append(arrays, fmt.Sprintf("$%d::%s[]", i+1, f.SQLType))
		if f.hasDefault() {
			sel = append(sel, "COALESCE(v."+f.quoted+", "+f.Default+")")
		} else {
			sel = append(sel, "v."+f.quoted)
		}
	}
	for _, f := range m.Fields {
		if f.Auto || f.hasDefault() || f.Version {
			returning = append(returning, f)
		}
	}
	var b strings.Builder
	b.WriteString("INSERT INTO " + m.quotedTable + " (" + strings.Join(cols, ", ") + ") SELECT " + strings.Join(sel, ", ") +
		" FROM unnest(" + strings.Join(arrays, ", ") + ") WITH ORDINALITY AS v(" + strings.Join(names, ", ") + ", ord) ORDER BY v.ord")
	writeReturning(&b, returning)
	plan := &bulkPlan{sql: b.String(), cols: c.insertable, returning: returning}
	actual, _ := c.bulk.LoadOrStore(struct{}{}, plan)
	pl := actual.(*bulkPlan)
	return pl.sql, pl.cols, pl.returning
}

type bulkPlan struct {
	sql       string
	cols      []*Field
	returning []*Field
}
