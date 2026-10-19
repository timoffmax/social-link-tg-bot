package app

import (
	"errors"
	"testing"
)

// clearDatabaseEnv unsets every DB_* variable.
func clearDatabaseEnv(t *testing.T) {
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_SSLMODE"} {
		t.Setenv(key, "")
	}
}

func TestUnavailableDatabaseErrIsNilWithoutDatabaseConfigured(t *testing.T) {
	clearDatabaseEnv(t)
	dbErr := errors.New("failed to ping database: connection refused")

	if err := unavailableDatabaseErr(false, dbErr); err != nil {
		t.Fatalf("no database configured must keep the settings inert, got %v", err)
	}
}

func TestUnavailableDatabaseErrIsTheStartErrorWhenConfigured(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("DB_HOST", "db.internal")
	dbErr := errors.New("failed to ping database: connection refused")

	if err := unavailableDatabaseErr(false, dbErr); err != dbErr {
		t.Fatalf("a configured database that failed must pass its error, got %v", err)
	}
}

func TestUnavailableDatabaseErrIsNilWithADatabase(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("DB_HOST", "db.internal")
	migrationErr := errors.New("failed to run migrations")

	if err := unavailableDatabaseErr(true, migrationErr); err != nil {
		t.Fatalf("an opened database must be used as before, got %v", err)
	}

	if err := unavailableDatabaseErr(true, nil); err != nil {
		t.Fatalf("an opened database must be used as before, got %v", err)
	}
}
