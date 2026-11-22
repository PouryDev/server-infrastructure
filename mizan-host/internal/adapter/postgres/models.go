package postgres

// Models lists every row struct of the schema. `migrate diff` compares them with the migrations and
// writes the difference as a new migration; add each new row struct here.
func Models() []any {
	return []any{userRow{}}
}
