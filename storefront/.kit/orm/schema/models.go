package schema

import (
	"fmt"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/orm"
)

// FromModels builds the desired schema from row structs (values, pointers or reflect types). Relations
// become foreign keys: belongs_to adds one on its own table, has_one / has_many on the target's table,
// and many_to_many creates the join table. A relation to a model that is not in the list is an error.
func FromModels(models ...any) (*Schema, error) {
	s := newSchema()
	described := map[string]*orm.Model{}
	for _, v := range models {
		m, err := orm.Describe(v)
		if err != nil {
			return nil, err
		}
		if _, dup := described[m.Table]; dup {
			return nil, fmt.Errorf("schema: table %q is defined by two models", m.Table)
		}
		described[m.Table] = m
		t, err := tableOf(m)
		if err != nil {
			return nil, err
		}
		s.Tables[m.Table] = t
	}

	joins := map[string]bool{}
	byType := map[any]*orm.Model{}
	for _, m := range described {
		byType[m.Type] = m
	}
	for _, m := range described {
		for _, rel := range m.Relations {
			target, ok := byType[rel.Target]
			if !ok {
				return nil, fmt.Errorf("schema: %s.%s relates to %s, which is not in the model list", m.Type.Name(), rel.Name, rel.Target.Name())
			}
			if err := addRelation(s, joins, m, target, rel); err != nil {
				return nil, err
			}
		}
	}
	for _, t := range s.Tables {
		for _, fk := range t.FKs {
			if err := checkName("foreign key", fk.Name); err != nil {
				return nil, err
			}
		}
		for _, ix := range t.Indexes {
			if err := checkName("index", ix.Name); err != nil {
				return nil, err
			}
		}
		for _, u := range t.Uniques {
			if err := checkName("unique constraint", u.Name); err != nil {
				return nil, err
			}
		}
		for _, c := range t.Checks {
			if err := checkName("check constraint", c.Name); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func tableOf(m *orm.Model) (*Table, error) {
	t := &Table{Name: m.Table, PK: m.PKColumns(), Was: m.WasTable}
	for _, c := range m.Checks {
		t.Checks = append(t.Checks, &Check{Name: c.Name, Expr: c.Expr})
	}
	groups := map[string][]string{}
	var groupOrder []string
	for _, f := range m.Fields {
		c := &Column{Name: f.Column, Type: NormalizeType(f.SQLType), NotNull: !f.Nullable, Default: f.Default, Identity: f.Auto, Was: f.Was}
		t.Columns = append(t.Columns, c)
		if f.Unique {
			t.Uniques = append(t.Uniques, &Unique{Name: m.Table + "_" + f.Column + "_key", Columns: []string{f.Column}, Constraint: true})
		}
		if f.Index_ != "" {
			name := f.Index_
			if name == "auto" {
				name = "idx_" + m.Table + "_" + f.Column
			}
			if _, seen := groups[name]; !seen {
				groupOrder = append(groupOrder, name)
			}
			groups[name] = append(groups[name], f.Column)
		}
	}
	for _, name := range groupOrder {
		t.Indexes = append(t.Indexes, &Index{Name: name, Columns: groups[name]})
	}
	return t, nil
}

func addFK(t *Table, fk *ForeignKey) {
	for _, e := range t.FKs {
		if strings.Join(e.Columns, ",") == strings.Join(fk.Columns, ",") && e.RefTable == fk.RefTable {
			if e.OnDelete == "" {
				e.OnDelete = fk.OnDelete // the side that spells out ondelete wins
			}
			return
		}
	}
	t.FKs = append(t.FKs, fk)
}

func pkColumn(m *orm.Model, ref string) (*orm.Field, error) {
	if ref != "" {
		f, ok := m.Field(ref)
		if !ok {
			return nil, fmt.Errorf("schema: %s has no column %q", m.Type.Name(), ref)
		}
		return f, nil
	}
	if len(m.PK) != 1 {
		return nil, fmt.Errorf("schema: relations need a single-column primary key on %s (or an explicit ref=)", m.Type.Name())
	}
	return m.PK[0], nil
}

func addRelation(s *Schema, joins map[string]bool, m, target *orm.Model, rel *orm.Relation) error {
	onDelete := strings.ToLower(rel.OnDelete)
	if onDelete == "no action" {
		onDelete = ""
	}
	switch rel.Kind {
	case orm.BelongsTo:
		ref, err := pkColumn(target, rel.Ref)
		if err != nil {
			return err
		}
		addFK(s.Tables[m.Table], &ForeignKey{Name: "fk_" + m.Table + "_" + rel.FK, Columns: []string{rel.FK},
			RefTable: target.Table, RefColumns: []string{ref.Column}, OnDelete: onDelete})
	case orm.HasOne, orm.HasMany:
		ref, err := pkColumn(m, rel.Ref)
		if err != nil {
			return err
		}
		if _, ok := target.Field(rel.FK); !ok {
			return fmt.Errorf("schema: %s.%s: %s has no column %q", m.Type.Name(), rel.Name, target.Type.Name(), rel.FK)
		}
		addFK(s.Tables[target.Table], &ForeignKey{Name: "fk_" + target.Table + "_" + rel.FK, Columns: []string{rel.FK},
			RefTable: m.Table, RefColumns: []string{ref.Column}, OnDelete: onDelete})
	case orm.ManyToMany:
		mref, err := pkColumn(m, rel.Ref)
		if err != nil {
			return err
		}
		tref, err := pkColumn(target, "")
		if err != nil {
			return err
		}
		join := &Table{Name: rel.JoinTable, PK: []string{rel.JoinFK, rel.JoinRef}}
		join.Columns = []*Column{
			{Name: rel.JoinFK, Type: NormalizeType(mref.SQLType), NotNull: true},
			{Name: rel.JoinRef, Type: NormalizeType(tref.SQLType), NotNull: true},
		}
		join.FKs = []*ForeignKey{
			{Name: "fk_" + rel.JoinTable + "_" + rel.JoinFK, Columns: []string{rel.JoinFK}, RefTable: m.Table, RefColumns: []string{mref.Column}, OnDelete: "cascade"},
			{Name: "fk_" + rel.JoinTable + "_" + rel.JoinRef, Columns: []string{rel.JoinRef}, RefTable: target.Table, RefColumns: []string{tref.Column}, OnDelete: "cascade"},
		}
		// The primary key serves lookups by the first column; the second needs its own index.
		join.Indexes = []*Index{{Name: "idx_" + rel.JoinTable + "_" + rel.JoinRef, Columns: []string{rel.JoinRef}}}
		if _, ok := s.Tables[rel.JoinTable]; ok {
			if joins[rel.JoinTable] {
				return nil // declared from both sides of the relation
			}
			return fmt.Errorf("schema: join table %q collides with a model table of the same name", rel.JoinTable)
		}
		s.Tables[rel.JoinTable] = join
		joins[rel.JoinTable] = true
	}
	return nil
}
