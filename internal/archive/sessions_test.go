package archive

import (
	"errors"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0 }
	if err := s.CreateSession("hash-a", "csrf-a", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Session("hash-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.CSRF != "csrf-a" || !got.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("session = %+v", got)
	}
	if _, err := s.Session("hash-b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session error = %v, want ErrNotFound", err)
	}
}

func TestExpiredSessionIsNotFound(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0 }
	if err := s.CreateSession("hash-a", "csrf-a", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(time.Hour) } // exactly at expiry
	if _, err := s.Session("hash-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}
}

func TestDeleteSessions(t *testing.T) {
	s := openTemp(t)
	for _, h := range []string{"a", "b", "c"} {
		if err := s.CreateSession(h, "csrf", s.now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteSession("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("never-existed"); err != nil {
		t.Fatalf("deleting an unknown session: %v", err)
	}
	if _, err := s.Session("a"); !errors.Is(err, ErrNotFound) {
		t.Error("session a survived DeleteSession")
	}
	if _, err := s.Session("b"); err != nil {
		t.Errorf("session b was deleted too: %v", err)
	}
	if err := s.DeleteAllSessions(); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"b", "c"} {
		if _, err := s.Session(h); !errors.Is(err, ErrNotFound) {
			t.Errorf("session %s survived DeleteAllSessions", h)
		}
	}
}
