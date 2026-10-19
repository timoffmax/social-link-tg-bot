package app

import (
	"database/sql"
	"testing"

	"github.com/timoffmax/social-link-bot/services"
)

// markDatabaseOpenForTest makes the bot see a database with a handle that is
// never connected (lib/pq connects lazily), for texts that only check it.
func markDatabaseOpenForTest(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 sslmode=disable connect_timeout=1")

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	restore := services.UseDatabaseHandle(db)

	t.Cleanup(func() {
		restore()
		db.Close()
	})
}
