package orm_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PouryDev/server-infrastructure/gokit/migrate"
	"github.com/PouryDev/server-infrastructure/gokit/orm"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Database benchmarks: run with `scripts/gokit.sh bench-db`. They compare the ORM with hand-written pgx
// on the same data so the overhead of the abstraction is a number, not a feeling.

type benchEnv struct {
	url string
	db  *postgres.DB
	ids []uuid.UUID
}

var (
	benchOnce sync.Once
	benchRes  *benchEnv
	benchErr  error
)

func benchDB(b *testing.B) *benchEnv {
	base := os.Getenv("GOKIT_TEST_DATABASE_URL")
	if base == "" {
		b.Skip("GOKIT_TEST_DATABASE_URL not set")
	}
	benchOnce.Do(func() {
		ctx := context.Background()
		admin, err := pgx.Connect(ctx, base)
		if err != nil {
			benchErr = err
			return
		}
		raw := make([]byte, 4)
		_, _ = rand.Read(raw)
		name := "gokit_bench_" + hex.EncodeToString(raw)
		if _, benchErr = admin.Exec(ctx, "CREATE DATABASE "+name); benchErr != nil {
			return
		}
		u, _ := url.Parse(base)
		u.Path = "/" + name
		dir, _ := os.MkdirTemp("", "gokit-bench")
		if _, benchErr = migrate.Diff(ctx, migrate.DiffOptions{URL: u.String(), FS: fstest.MapFS{}, Models: allModels, OutDir: dir, Name: "init"}); benchErr != nil {
			return
		}
		if _, benchErr = (migrate.Runner{URL: u.String(), FS: os.DirFS(dir)}).Up(ctx); benchErr != nil {
			return
		}
		db, err := postgres.Connect(ctx, postgres.Config{URL: u.String(), PreparedStatements: true})
		if err != nil {
			benchErr = err
			return
		}
		authors, _ := orm.New[authorRow](db)
		list := make([]authorRow, 20000)
		for i := range list {
			list[i] = authorRow{Name: fmt.Sprintf("Author %d", i), Email: fmt.Sprintf("a%d@example.com", i)}
		}
		if benchErr = authors.CreateMany(ctx, list); benchErr != nil {
			return
		}
		ids := make([]uuid.UUID, len(list))
		for i := range list {
			ids[i] = list[i].ID
		}
		// 3 posts for each of the first 100 authors, for the preload benchmark.
		posts, _ := orm.New[postRow](db)
		var ps []postRow
		for _, id := range ids[:100] {
			for j := range 3 {
				ps = append(ps, postRow{AuthorID: id, Title: fmt.Sprintf("post %d", j)})
			}
		}
		benchErr = posts.CreateMany(ctx, ps)
		benchRes = &benchEnv{url: u.String(), db: db, ids: ids}
	})
	if benchErr != nil {
		b.Fatal(benchErr)
	}
	return benchRes
}

func BenchmarkGetByID(b *testing.B) {
	env := benchDB(b)
	ctx := context.Background()
	authors, _ := orm.New[authorRow](env.db)
	const rawSQL = `SELECT id, name, email, bio, secret, created_at, updated_at, deleted_at, version FROM authors WHERE id = $1 AND deleted_at IS NULL`

	b.Run("orm", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := authors.Get(ctx, env.ids[i%1000]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("pgx-raw", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			var a authorRow
			err := env.db.Pool().QueryRow(ctx, rawSQL, env.ids[i%1000]).Scan(&a.ID, &a.Name, &a.Email, &a.Bio, &a.Secret, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &a.Version)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("postgres.SelectOne", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := postgres.SelectOne[authorRow](ctx, env.db.Pool(), rawSQL, env.ids[i%1000]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkList100(b *testing.B) {
	env := benchDB(b)
	ctx := context.Background()
	authors, _ := orm.New[authorRow](env.db)
	const rawSQL = `SELECT id, name, email, bio, secret, created_at, updated_at, deleted_at, version FROM authors WHERE deleted_at IS NULL ORDER BY id LIMIT 100`

	b.Run("orm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := authors.Query().OrderBy("id").Limit(100).All(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("pgx-raw", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := env.db.Pool().Query(ctx, rawSQL)
			if err != nil {
				b.Fatal(err)
			}
			out := make([]authorRow, 0, 100)
			for rows.Next() {
				var a authorRow
				if err := rows.Scan(&a.ID, &a.Name, &a.Email, &a.Bio, &a.Secret, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &a.Version); err != nil {
					b.Fatal(err)
				}
				out = append(out, a)
			}
			rows.Close()
			if rows.Err() != nil {
				b.Fatal(rows.Err())
			}
			_ = out
		}
	})
	b.Run("postgres.Select", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := postgres.Select[authorRow](ctx, env.db.Pool(), rawSQL); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkInsertMany1000(b *testing.B) {
	env := benchDB(b)
	ctx := context.Background()
	authors, _ := orm.New[authorRow](env.db)
	mk := func(n int, tag string) []authorRow {
		l := make([]authorRow, n)
		for i := range l {
			l[i] = authorRow{Name: "x", Email: fmt.Sprintf("%s-%d@example.com", tag, i)}
		}
		return l
	}
	b.Run("orm-CreateMany", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if err := authors.CreateMany(ctx, mk(1000, fmt.Sprintf("cm%d", i))); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("orm-Create-loop", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			l := mk(1000, fmt.Sprintf("cl%d", i))
			for j := range l {
				if err := authors.Create(ctx, &l[j]); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("pgx-CopyFrom", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			l := mk(1000, fmt.Sprintf("cf%d", i))
			rows := make([][]any, len(l))
			now := time.Now()
			for j, a := range l {
				rows[j] = []any{uuid.New(), a.Name, a.Email, "x", now, now, 1}
			}
			_, err := env.db.Pool().CopyFrom(ctx, pgx.Identifier{"authors"}, []string{"id", "name", "email", "secret", "created_at", "updated_at", "version"}, pgx.CopyFromRows(rows))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPreload(b *testing.B) {
	env := benchDB(b)
	ctx := context.Background()
	authors, _ := orm.New[authorRow](env.db)
	posts, _ := orm.New[postRow](env.db)

	b.Run("orm-Preload-2-queries", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := authors.Query().OrderBy("id").Limit(100).Preload("Posts").All(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("n+1-101-queries", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			as, err := authors.Query().OrderBy("id").Limit(100).All(ctx)
			if err != nil {
				b.Fatal(err)
			}
			for i := range as {
				as[i].Posts, err = posts.Query().Where(orm.Eq("author_id", as[i].ID)).All(ctx)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

func BenchmarkDeepPage(b *testing.B) {
	env := benchDB(b)
	ctx := context.Background()
	authors, _ := orm.New[authorRow](env.db)
	sch, _ := authors.Schema()
	deep, _ := sch.Restrict() // same limits
	_ = deep
	big, _ := orm.New[authorRow](env.db, orm.WithSchemaOptions(query.WithLimits(20, 100, 3, 1_000_000)))

	// Offset pagination reads and discards every skipped row; keyset seeks.
	b.Run("offset-page-300", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := big.Query().Paginate(ctx, query.Params{Page: 300, PageSize: 50}, postgres.WithoutTotal()); err != nil {
				b.Fatal(err)
			}
		}
	})
	// A cursor for the same position: walk to it once, outside the timer.
	cursor := ""
	for range 299 {
		p, err := authors.Query().Keyset(ctx, query.Params{PageSize: 50}, cursor)
		if err != nil {
			b.Fatal(err)
		}
		cursor = p.NextCursor
	}
	b.Run("keyset-page-300", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := authors.Query().Keyset(ctx, query.Params{PageSize: 50}, cursor); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkJoin(b *testing.B) {
	env := benchDB(b)
	ctx := context.Background()
	posts, _ := orm.New[postRow](env.db)
	authors, _ := orm.New[authorRow](env.db)

	const joinSQL = `SELECT p.id, p.author_id, p.title, p.views, p.rating, p.labels, p.meta FROM posts p
		JOIN authors a ON a.id = p.author_id AND a.deleted_at IS NULL
		WHERE a.name = $1 ORDER BY p.views DESC, p.id LIMIT 20`
	scan := func(rows pgx.Rows) []postRow {
		out := make([]postRow, 0, 20)
		for rows.Next() {
			var p postRow
			if err := rows.Scan(&p.ID, &p.AuthorID, &p.Title, &p.Views, &p.Rating, &p.Labels, &p.Meta); err != nil {
				b.Fatal(err)
			}
			out = append(out, p)
		}
		rows.Close()
		return out
	}

	b.Run("join/orm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := posts.Query().Join("Author").Where(orm.Eq("Author.name", "Author 7")).OrderByDesc("views").OrderBy("id").Limit(20).All(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("join/pgx-raw", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := env.db.Pool().Query(ctx, joinSQL, "Author 7")
			if err != nil {
				b.Fatal(err)
			}
			scan(rows)
		}
	})
	b.Run("has-exists/orm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := posts.Query().Where(orm.Has("Author", orm.Eq("name", "Author 7"))).OrderByDesc("views").OrderBy("id").Limit(20).All(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	type stat struct {
		Author string `db:"author"`
		Posts  int64  `db:"posts"`
	}
	b.Run("aggregate/orm-Project", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := orm.Project[stat](ctx, authors.Query().Join("Posts").GroupBy("name").Having(orm.Gt("posts", 2)).OrderBy("author").Limit(50),
				orm.Col("name", "author"), orm.Count("*", "posts")); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("aggregate/orm-Fetch", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := orm.Fetch[stat](ctx, env.db, `SELECT a.name AS author, count(*) AS posts FROM authors a JOIN posts p ON p.author_id = a.id
				WHERE a.deleted_at IS NULL GROUP BY a.name HAVING count(*) > 2 ORDER BY a.name LIMIT 50`); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("aggregate/pgx-raw", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := env.db.Pool().Query(ctx, `SELECT a.name, count(*) FROM authors a JOIN posts p ON p.author_id = a.id
				WHERE a.deleted_at IS NULL GROUP BY a.name HAVING count(*) > 2 ORDER BY a.name LIMIT 50`)
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
				var s stat
				if err := rows.Scan(&s.Author, &s.Posts); err != nil {
					b.Fatal(err)
				}
			}
			rows.Close()
		}
	})
}
