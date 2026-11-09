package orm_test

import (
	"time"

	"github.com/google/uuid"
)

// Test models: authors have many posts, posts belong to an author, have many comments and many tags.

type authorRow struct {
	ID        uuid.UUID  `db:"id" json:"id" orm:"pk;default=gen_random_uuid()"`
	Name      string     `db:"name" json:"name" orm:"size=100"`
	Email     string     `db:"email" json:"email" orm:"unique"`
	Bio       *string    `db:"bio" json:"bio"`
	Secret    string     `db:"secret" json:"-" orm:"default='x'" query:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at" orm:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at" orm:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at" orm:"soft_delete"`
	Version   int        `db:"version" json:"version" orm:"version;default=1"`
	Posts     []postRow  `orm:"rel=has_many;fk=author_id"`
}

type postRow struct {
	ID       int64          `db:"id" json:"id" orm:"pk;auto"`
	AuthorID uuid.UUID      `db:"author_id" json:"author_id" orm:"index"`
	Title    string         `db:"title" json:"title" orm:"size=200"`
	Views    int            `db:"views" json:"views" orm:"default=0"`
	Rating   float64        `db:"rating" json:"rating" orm:"default=0"`
	Labels   []string       `db:"labels" json:"labels" orm:"default='{}'"`
	Meta     map[string]any `db:"meta" json:"-" orm:"null"`
	Author   *authorRow     `orm:"rel=belongs_to;fk=author_id;ondelete=cascade"`
	Comments []commentRow   `orm:"rel=has_many;fk=post_id"`
	Tags     []tagRow       `orm:"rel=many_to_many"`
}

type tagRow struct {
	ID    int64     `db:"id" json:"id" orm:"pk;auto"`
	Name  string    `db:"name" json:"name" orm:"unique"`
	Posts []postRow `orm:"rel=many_to_many"`
}

type commentRow struct {
	ID     int64  `db:"id" json:"id" orm:"pk;auto"`
	PostID int64  `db:"post_id" json:"post_id" orm:"index"`
	Body   string `db:"body" json:"body"`
}
