package signin

import (
	"context"
	"errors"
	"testing"
	"time"
)

// runContract exercises the behaviour every Store must share. expire moves
// the given session past its expiry (by clock or by SQL, per store).
func runContract(t *testing.T, newStore func(t *testing.T) Store, expire func(t *testing.T, s Store, id string)) {
	ctx := context.Background()

	t.Run("start then latest", func(t *testing.T) {
		s := newStore(t)
		sess, err := s.Start(ctx, "a1", "hash-a1")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if sess.State != StateWaitingForPod || sess.ID == "" || !sess.ExpiresAt.After(sess.CreatedAt) {
			t.Fatalf("started session = %+v", sess)
		}
		got, err := s.Latest(ctx, "a1")
		if err != nil || got.ID != sess.ID {
			t.Fatalf("Latest = %+v, %v; want %s", got, err, sess.ID)
		}
		if _, err := s.Latest(ctx, "other"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Latest(other) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("second start conflicts", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Start(ctx, "a1", "hash-a1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Start(ctx, "a1", "hash-a1"); !errors.Is(err, ErrLive) {
			t.Fatalf("second Start err = %v, want ErrLive", err)
		}
		if _, err := s.Start(ctx, "a2", "hash-a2"); err != nil {
			t.Fatalf("Start for another agent: %v", err)
		}
	})

	t.Run("full flow hands the redirect once", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		if err := s.SubmitRedirect(ctx, "a1", "http://127.0.0.1:1455/auth/callback?code=c"); !errors.Is(err, ErrWrongState) {
			t.Fatalf("paste before the link exists: err = %v, want ErrWrongState", err)
		}
		if err := s.SetAuthURL(ctx, "a1", sess.ID, "https://auth.openai.com/x"); err != nil {
			t.Fatalf("SetAuthURL: %v", err)
		}
		if got, _ := s.Get(ctx, "a1", sess.ID); got.State != StateAwaitingRedirect || got.AuthURL != "https://auth.openai.com/x" {
			t.Fatalf("after SetAuthURL = %+v", got)
		}
		if url, err := s.TakeRedirect(ctx, "a1", sess.ID); err != nil || url != "" {
			t.Fatalf("TakeRedirect before paste = %q, %v; want empty", url, err)
		}
		if err := s.SubmitRedirect(ctx, "a1", "http://127.0.0.1:1455/auth/callback?code=c"); err != nil {
			t.Fatalf("SubmitRedirect: %v", err)
		}
		url, err := s.TakeRedirect(ctx, "a1", sess.ID)
		if err != nil || url != "http://127.0.0.1:1455/auth/callback?code=c" {
			t.Fatalf("TakeRedirect = %q, %v", url, err)
		}
		if again, _ := s.TakeRedirect(ctx, "a1", sess.ID); again != "" {
			t.Fatalf("second TakeRedirect = %q, want empty: the URL is handed out once", again)
		}
		if got, _ := s.Get(ctx, "a1", sess.ID); got.State != StateExchanging {
			t.Fatalf("after TakeRedirect state = %s, want exchanging", got.State)
		}
		if err := s.Finish(ctx, "a1", sess.ID, true, ""); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if got, _ := s.Get(ctx, "a1", sess.ID); got.State != StateSucceeded {
			t.Fatalf("after Finish state = %s, want succeeded", got.State)
		}
		if _, err := s.Start(ctx, "a1", "hash-a1"); err != nil {
			t.Fatalf("Start after a finished session: %v", err)
		}
	})

	t.Run("failure keeps the message", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		if err := s.Finish(ctx, "a1", sess.ID, false, "OAuth state mismatch"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, "a1", sess.ID)
		if got.State != StateFailed || got.Error != "OAuth state mismatch" {
			t.Fatalf("failed session = %+v", got)
		}
		if err := s.SetAuthURL(ctx, "a1", sess.ID, "x"); !errors.Is(err, ErrClosed) {
			t.Fatalf("SetAuthURL on a finished session: err = %v, want ErrClosed", err)
		}
	})

	t.Run("cancel closes the live session", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		if err := s.Cancel(ctx, "a1"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Get(ctx, "a1", sess.ID); got.State != StateCancelled {
			t.Fatalf("state = %s, want cancelled", got.State)
		}
		if _, err := s.TakeRedirect(ctx, "a1", sess.ID); !errors.Is(err, ErrClosed) {
			t.Fatalf("TakeRedirect after cancel: err = %v, want ErrClosed", err)
		}
	})

	t.Run("expired session reads expired and frees the agent", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		expire(t, s, sess.ID)
		got, err := s.Get(ctx, "a1", sess.ID)
		if err != nil || got.State != StateExpired {
			t.Fatalf("expired session = %+v, %v; want state expired", got, err)
		}
		if _, err := s.TakeRedirect(ctx, "a1", sess.ID); !errors.Is(err, ErrClosed) {
			t.Fatalf("TakeRedirect on an expired session: err = %v, want ErrClosed", err)
		}
		if _, err := s.Start(ctx, "a1", "hash-a1"); err != nil {
			t.Fatalf("Start after expiry: %v", err)
		}
	})

	t.Run("an exchange in progress can finish after the session expired", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		_ = s.SetAuthURL(ctx, "a1", sess.ID, "https://auth.openai.com/x")
		_ = s.SubmitRedirect(ctx, "a1", "http://127.0.0.1:1455/auth/callback?code=c")
		if _, err := s.TakeRedirect(ctx, "a1", sess.ID); err != nil {
			t.Fatal(err)
		}
		expire(t, s, sess.ID)
		if err := s.Finish(ctx, "a1", sess.ID, true, ""); err != nil {
			t.Fatalf("Finish of a slow exchange: %v, want the success recorded", err)
		}
		if got, _ := s.Get(ctx, "a1", sess.ID); got.State != StateSucceeded {
			t.Fatalf("state = %s, want succeeded", got.State)
		}
	})

	t.Run("cancel does not abort an exchange in progress", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		_ = s.SetAuthURL(ctx, "a1", sess.ID, "https://auth.openai.com/x")
		_ = s.SubmitRedirect(ctx, "a1", "http://127.0.0.1:1455/auth/callback?code=c")
		_, _ = s.TakeRedirect(ctx, "a1", sess.ID)
		if err := s.Cancel(ctx, "a1"); err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(ctx, "a1", sess.ID, true, ""); err != nil {
			t.Fatalf("Finish after a cancel during the exchange: %v", err)
		}
		if got, _ := s.Get(ctx, "a1", sess.ID); got.State != StateSucceeded {
			t.Fatalf("state = %s, want succeeded", got.State)
		}
	})

	t.Run("keeps the pod token hash", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		got, err := s.Get(ctx, "a1", sess.ID)
		if err != nil || got.PodTokenHash != "hash-a1" {
			t.Fatalf("PodTokenHash = %q, %v; want hash-a1", got.PodTokenHash, err)
		}
	})

	t.Run("get is scoped to the agent", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		if _, err := s.Get(ctx, "a2", sess.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get with another agent's name: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("prune removes old sessions", func(t *testing.T) {
		s := newStore(t)
		sess, _ := s.Start(ctx, "a1", "hash-a1")
		_ = s.Finish(ctx, "a1", sess.ID, true, "")
		n, err := s.Prune(ctx, time.Now().Add(time.Hour))
		if err != nil || n != 1 {
			t.Fatalf("Prune = %d, %v; want 1", n, err)
		}
		if _, err := s.Latest(ctx, "a1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Latest after prune: err = %v, want ErrNotFound", err)
		}
	})
}

func TestMemStoreContract(t *testing.T) {
	clocks := map[Store]*time.Time{}
	runContract(t,
		func(t *testing.T) Store {
			now := time.Now()
			s := NewMemStore(func() time.Time { return now })
			clocks[s] = &now
			return s
		},
		func(t *testing.T, s Store, _ string) {
			*clocks[s] = clocks[s].Add(TTL + time.Second)
		})
}
