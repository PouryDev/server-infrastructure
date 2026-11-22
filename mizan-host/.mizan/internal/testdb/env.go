package testdb

import "os"

func lookup() string { return os.Getenv("MIZAN_TEST_DATABASE_URL") }
