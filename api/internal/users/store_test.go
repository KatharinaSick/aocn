package users

import (
	"context"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/db/dbtest"
)

// testStore gives each test an empty database of its own.
func testStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(dbtest.Pool(t, "users"))
}

func TestSaveKeepsTheNewestLogin(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)

	if err := store.Save(ctx, User{GitHubID: 8, Login: "old", AvatarURL: "https://example.com/a.png"}, at); err != nil {
		t.Fatal(err)
	}
	renamed := User{GitHubID: 8, Login: "new", AvatarURL: "https://example.com/b.png"}
	if err := store.Save(ctx, renamed, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, 8)
	if err != nil || got != renamed {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGetUnknownUser(t *testing.T) {
	store := testStore(t)
	if _, err := store.Get(context.Background(), 404); err != ErrNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestByLoginIgnoresCaseAndFindsWhoHasItNow(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)

	// 8 was octo, renamed; 9 took the login later. 8's row still says octo until they log in
	// or verify again.
	if err := store.Save(ctx, User{GitHubID: 8, Login: "octo", AvatarURL: "a"}, at); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, User{GitHubID: 9, Login: "Octo", AvatarURL: "b"}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, since, err := store.ByLogin(ctx, "OCTO")
	if err != nil || got.GitHubID != 9 || got.Login != "Octo" || !since.Equal(at.Add(time.Hour)) {
		t.Fatalf("got %+v since %v, %v", got, since, err)
	}
	if _, _, err := store.ByLogin(ctx, "nobody"); err != ErrNotFound {
		t.Errorf("unknown login: %v", err)
	}
}
