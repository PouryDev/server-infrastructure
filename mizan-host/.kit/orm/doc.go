// Package orm is a small, explicit, fast object-relational mapper for PostgreSQL.
//
// Design goals, in order: predictable SQL (no hidden queries: relations load only when you Preload,
// and then with one query per relation, never one per row), low overhead (model metadata is built
// once per type, SQL for CRUD is pre-built, rows are scanned straight into struct fields, bulk
// inserts are a single statement), and safety (identifiers come from validated metadata, values are
// always bind arguments).
//
// It belongs in the persistence adapter of a clean-architecture project: define a row struct with
// tags next to the SQL, map it to the domain entity, and keep the domain free of any tag or import
// from this package.
//
//	type authorRow struct {
//	    ID    uuid.UUID `db:"id" orm:"pk;default=gen_random_uuid()"`
//	    Name  string    `db:"name" orm:"size=100"`
//	    Email string    `db:"email" orm:"unique"`
//	    Posts []postRow `orm:"rel=has_many;fk=author_id"`
//	}
//	type postRow struct {
//	    ID       int64      `db:"id" orm:"pk;auto"`
//	    AuthorID uuid.UUID  `db:"author_id" orm:"index"`
//	    Title    string     `db:"title"`
//	    Author   *authorRow `orm:"rel=belongs_to;fk=author_id;ondelete=cascade"`
//	    Tags     []tagRow   `orm:"rel=many_to_many;join=post_tags"`
//	}
//
// Tags (`orm:"a;b=c"`, separated by semicolons):
//
//	columns   pk  auto (database generated identity)  default=<sql>  unique  index | index=<name>  check=<sql>
//	          size=<n>  type=<sql type>  null  notnull  readonly  created_at  updated_at  soft_delete  version
//	          was=<old column name> (rename)  -
//	relations rel=belongs_to|has_one|has_many|many_to_many  fk=  ref=  join=  joinfk=  joinref=  ondelete=
//
// Column names come from `db:"..."` (default: snake_case of the field name); `json` and `query` tags
// control how the field appears in client-driven filtering (see package query).
package orm
