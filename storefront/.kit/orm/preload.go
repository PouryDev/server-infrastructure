package orm

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
)

// preload loads the named relations into items. It works on reflect values so nested paths and any
// target type share one implementation; each relation costs one query for all parents.
func (r *Repository[T]) preload(ctx context.Context, items []T, names []string) error {
	if len(items) == 0 || len(names) == 0 {
		return nil
	}
	return preloadInto(ctx, r.conn.Querier(ctx), r.m, reflect.ValueOf(items), names)
}

// preloadInto loads relations for the slice sv (of m.Type) and recurses for dotted paths.
func preloadInto(ctx context.Context, q postgres.Querier, m *Model, sv reflect.Value, names []string) error {
	// Group "Posts", "Posts.Comments", "Author" by their first segment.
	groups := map[string][]string{}
	var order []string
	for _, n := range names {
		head, rest, _ := strings.Cut(n, ".")
		if _, seen := groups[head]; !seen {
			order = append(order, head)
		}
		if rest != "" {
			groups[head] = append(groups[head], rest)
		} else if _, seen := groups[head]; !seen {
			groups[head] = nil
		}
	}
	for _, head := range order {
		rel, ok := m.Relation(head)
		if !ok {
			return fmt.Errorf("orm: %s has no relation %q", m.Type.Name(), head)
		}
		if err := loadRelation(ctx, q, m, sv, rel, groups[head]); err != nil {
			return err
		}
	}
	return nil
}

// key turns a column value into a comparable map key (nil for a NULL pointer).
func key(v reflect.Value) any {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(v.Uint())
	}
	return v.Interface() // string, uuid.UUID ([16]byte) and friends are comparable
}

// distinct collects the non-null, de-duplicated key values of field f over the slice, as a typed slice
// of the field's base type (what pgx needs for = ANY($1)).
func distinct(sv reflect.Value, f *Field) (any, bool) {
	base := f.Type
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	seen := map[any]struct{}{}
	out := reflect.MakeSlice(reflect.SliceOf(base), 0, sv.Len())
	for i := range sv.Len() {
		fv := sv.Index(i).FieldByIndex(f.Index)
		k := key(fv)
		if k == nil {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		for fv.Kind() == reflect.Pointer {
			fv = fv.Elem()
		}
		out = reflect.Append(out, fv)
	}
	return out.Interface(), out.Len() > 0
}

func columnOf(m *Model, name string) (*Field, error) {
	f, ok := m.Field(name)
	if !ok {
		return nil, fmt.Errorf("orm: %s has no column %q", m.Type.Name(), name)
	}
	return f, nil
}

// refColumn is the referenced column: explicit ref=, else the primary key of m.
func refColumn(m *Model, ref string) (*Field, error) {
	if ref != "" {
		return columnOf(m, ref)
	}
	if len(m.PK) != 1 {
		return nil, fmt.Errorf("orm: relations need a single-column primary key on %s (or an explicit ref=)", m.Type.Name())
	}
	return m.PK[0], nil
}

func selectWhereAny(m *Model, col *Field) string {
	return "SELECT " + m.cache.selectList + " FROM " + m.quotedTable + " WHERE " + col.quoted + " = ANY($1)" + liveSuffix(m)
}

func liveSuffix(m *Model) string {
	if m.cache.live != "" {
		return " AND " + m.cache.live
	}
	return ""
}

func loadRelation(ctx context.Context, q postgres.Querier, m *Model, parents reflect.Value, rel *Relation, nested []string) error {
	target, err := Describe(rel.Target)
	if err != nil {
		return err
	}
	switch rel.Kind {
	case BelongsTo:
		fk, err := columnOf(m, rel.FK)
		if err != nil {
			return err
		}
		ref, err := refColumn(target, rel.Ref)
		if err != nil {
			return err
		}
		ids, found := distinct(parents, fk)
		if !found {
			return nil
		}
		children, err := queryTarget(ctx, q, target, selectWhereAny(target, ref), ids, nested)
		if err != nil {
			return err
		}
		byKey := indexBy(children, ref)
		for i := range parents.Len() {
			p := parents.Index(i)
			if child, ok := byKey[key(p.FieldByIndex(fk.Index))]; ok {
				assignOne(p.FieldByIndex(rel.Field), child, rel)
			}
		}
	case HasOne, HasMany:
		ref, err := refColumn(m, rel.Ref)
		if err != nil {
			return err
		}
		fk, err := columnOf(target, rel.FK)
		if err != nil {
			return err
		}
		ids, found := distinct(parents, ref)
		if !found {
			return nil
		}
		children, err := queryTarget(ctx, q, target, selectWhereAny(target, fk), ids, nested)
		if err != nil {
			return err
		}
		groups := map[any][]reflect.Value{}
		for i := range children.Len() {
			c := children.Index(i)
			k := key(c.FieldByIndex(fk.Index))
			groups[k] = append(groups[k], c)
		}
		for i := range parents.Len() {
			p := parents.Index(i)
			kids := groups[key(p.FieldByIndex(ref.Index))]
			if rel.Kind == HasOne {
				if len(kids) > 0 {
					assignOne(p.FieldByIndex(rel.Field), kids[0], rel)
				}
				continue
			}
			assignMany(p.FieldByIndex(rel.Field), kids, rel)
		}
	case ManyToMany:
		ref, err := refColumn(m, rel.Ref)
		if err != nil {
			return err
		}
		targetPK, err := refColumn(target, "")
		if err != nil {
			return err
		}
		ids, found := distinct(parents, ref)
		if !found {
			return nil
		}
		// 1) the join rows, 2) the targets of all distinct references.
		pairs, err := joinPairs(ctx, q, rel, ids, ref, targetPK)
		if err != nil {
			return err
		}
		if pairs.refs == nil {
			return nil
		}
		children, err := queryTarget(ctx, q, target, selectWhereAny(target, targetPK), pairs.refs, nested)
		if err != nil {
			return err
		}
		byKey := indexBy(children, targetPK)
		for i := range parents.Len() {
			p := parents.Index(i)
			var kids []reflect.Value
			for _, rk := range pairs.byParent[key(p.FieldByIndex(ref.Index))] {
				if c, ok := byKey[rk]; ok {
					kids = append(kids, c)
				}
			}
			assignMany(p.FieldByIndex(rel.Field), kids, rel)
		}
	}
	return nil
}

type joinResult struct {
	byParent map[any][]any // parent key -> target keys, in join-table order
	refs     any           // distinct target keys as a typed slice
}

func joinPairs(ctx context.Context, q postgres.Querier, rel *Relation, parentIDs any, parentRef, targetPK *Field) (joinResult, error) {
	sql := `SELECT "` + rel.JoinFK + `", "` + rel.JoinRef + `" FROM "` + rel.JoinTable + `" WHERE "` + rel.JoinFK + `" = ANY($1)`
	rows, err := q.Query(ctx, sql, parentIDs)
	if err != nil {
		return joinResult{}, postgres.MapError(err)
	}
	defer rows.Close()

	pbase, tbase := baseOf(parentRef.Type), baseOf(targetPK.Type)
	res := joinResult{byParent: map[any][]any{}}
	seen := map[any]struct{}{}
	refs := reflect.MakeSlice(reflect.SliceOf(tbase), 0, 8)
	for rows.Next() {
		pv, tv := reflect.New(pbase), reflect.New(tbase)
		if err := rows.Scan(pv.Interface(), tv.Interface()); err != nil {
			return joinResult{}, postgres.MapError(err)
		}
		pk, tk := key(pv.Elem()), key(tv.Elem())
		res.byParent[pk] = append(res.byParent[pk], tk)
		if _, dup := seen[tk]; !dup {
			seen[tk] = struct{}{}
			refs = reflect.Append(refs, tv.Elem())
		}
	}
	if err := rows.Err(); err != nil {
		return joinResult{}, postgres.MapError(err)
	}
	res.refs = refs.Interface()
	if refs.Len() == 0 {
		res.refs = nil
		return joinResult{byParent: res.byParent}, nil
	}
	return res, nil
}

func baseOf(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// queryTarget loads target rows with one query and preloads nested relations on them.
func queryTarget(ctx context.Context, q postgres.Querier, target *Model, sql string, ids any, nested []string) (reflect.Value, error) {
	var rows pgx.Rows
	rows, err := q.Query(ctx, sql, ids)
	if err != nil {
		return reflect.Value{}, postgres.MapError(err)
	}
	sv, err := scanReflect(rows, target, target.Fields, reflect.ValueOf(ids).Len())
	if err != nil {
		return reflect.Value{}, err
	}
	if len(nested) > 0 && sv.Len() > 0 {
		if err := preloadInto(ctx, q, target, sv, nested); err != nil {
			return reflect.Value{}, err
		}
	}
	return sv, nil
}

func indexBy(children reflect.Value, f *Field) map[any]reflect.Value {
	out := make(map[any]reflect.Value, children.Len())
	for i := range children.Len() {
		c := children.Index(i)
		out[key(c.FieldByIndex(f.Index))] = c
	}
	return out
}

// assignOne stores a copy of child into a single-valued relation field.
func assignOne(dst, child reflect.Value, rel *Relation) {
	if rel.Ptr {
		p := reflect.New(child.Type())
		p.Elem().Set(child)
		dst.Set(p)
		return
	}
	dst.Set(child)
}

// assignMany stores copies of kids into a slice relation field (an empty, non-nil slice when none).
func assignMany(dst reflect.Value, kids []reflect.Value, rel *Relation) {
	out := reflect.MakeSlice(dst.Type(), 0, len(kids))
	for _, c := range kids {
		if rel.Ptr {
			p := reflect.New(c.Type())
			p.Elem().Set(c)
			out = reflect.Append(out, p)
		} else {
			out = reflect.Append(out, c)
		}
	}
	dst.Set(out)
}
