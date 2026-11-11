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

	got, err := store.ByLogin(ctx, "OCTO")
	if err != nil || got.GitHubID != 9 || got.Login != "Octo" || !got.Since.Equal(at.Add(time.Hour)) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := store.ByLogin(ctx, "nobody"); err != ErrNotFound {
		t.Errorf("unknown login: %v", err)
	}
}

func TestProfilesArePrivateUntilMadePublic(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)

	// 8 was octo; 9 has it now. 8 making their card public must not move the login back.
	if err := store.Save(ctx, User{GitHubID: 8, Login: "octo", AvatarURL: "a"}, at); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, User{GitHubID: 9, Login: "octo", AvatarURL: "b"}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ByLogin(ctx, "octo"); err != nil || got.Public {
		t.Fatalf("new profile: %+v, %v", got, err)
	}

	if err := store.SetPublic(ctx, 8, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ByLogin(ctx, "octo"); err != nil || got.GitHubID != 9 || got.Public {
		t.Fatalf("after 8 went public: %+v, %v", got, err)
	}

	if err := store.SetPublic(ctx, 9, true); err != nil {
		t.Fatal(err)
	}
	// Logging in again keeps the choice.
	if err := store.Save(ctx, User{GitHubID: 9, Login: "octo", AvatarURL: "c"}, at.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ByLogin(ctx, "octo"); err != nil || !got.Public {
		t.Fatalf("after 9 went public and logged in again: %+v, %v", got, err)
	}

	if err := store.SetPublic(ctx, 404, true); err != ErrNotFound {
		t.Errorf("unknown user: %v", err)
	}
}
