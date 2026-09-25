package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lassegit/neolib/internal/database"
	"github.com/lassegit/neolib/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.New(db)
}

// The first account may only be created once, even when two signups arrive
// together; the unrestricted insert still works afterwards.
func TestCreateUserIfEmpty(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	first, err := st.CreateUserIfEmpty(ctx, "first@example.com", "First", "hash")
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	if first.ID == "" || first.Email != "first@example.com" {
		t.Fatalf("first user = %+v", first)
	}

	if _, err := st.CreateUserIfEmpty(ctx, "second@example.com", "Second", "hash"); !errors.Is(err, store.ErrSignupClosed) {
		t.Fatalf("second first-user insert error = %v, want ErrSignupClosed", err)
	}

	if _, err := st.CreateUser(ctx, "second@example.com", "Second", "hash"); err != nil {
		t.Fatalf("create second user: %v", err)
	}
}
