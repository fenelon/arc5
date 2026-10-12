package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noEnv(string) string { return "" }

func TestServeAnswersHealthAndStopsCleanly(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--listen", "127.0.0.1:0", "--data", dataDir}, noEnv, func(a string) { addr <- a })
	}()

	var listening string
	select {
	case listening = <-addr:
	case err := <-done:
		t.Fatalf("serve exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve never became ready")
	}

	resp, err := http.Get("http://" + listening + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "arc5.db")); err != nil {
		t.Fatalf("database not created: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
}

func TestServeFailsClearlyWhenPortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	err = run(context.Background(), []string{"serve", "--listen", ln.Addr().String(), "--data", t.TempDir()}, noEnv, nil)
	if err == nil || !strings.Contains(err.Error(), ln.Addr().String()) {
		t.Fatalf("error = %v, want one naming the address", err)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run(context.Background(), []string{"frobnicate"}, noEnv, nil); err == nil || !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("error = %v", err)
	}
	if err := run(context.Background(), nil, noEnv, nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("error with no command = %v", err)
	}
}
