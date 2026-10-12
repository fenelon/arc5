package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
	"github.com/fenelon/arc5/internal/search"
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

// seedArchive creates a data folder holding one unindexed message per text.
func seedArchive(t *testing.T, texts ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	store, err := archive.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	acc, err := store.CreateAccount("test", "Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertChat(acc, model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	for i, text := range texts {
		m := model.Message{ChatSourceID: "c1", SourceID: strconv.Itoa(i), SentAt: time.Unix(1_700_000_000+int64(i), 0), Text: text}
		if _, err := store.UpsertMessage(acc, m); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// hits opens the archive and counts what a query finds under the given languages.
func hits(t *testing.T, dir, query string, langs ...string) int {
	t.Helper()
	store, err := archive.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, err := search.NewAnalyzer(langs)
	if err != nil {
		t.Fatal(err)
	}
	page, err := search.New(store.Reader(), a).Search(context.Background(), search.Query{Text: query})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Hits)
}

func setting(t *testing.T, dir, key string) string {
	t.Helper()
	store, err := archive.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v, _, err := store.Setting(key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReindexCommandBuildsTheIndexForChosenLanguages(t *testing.T) {
	dir := seedArchive(t, "Собаки лают", "The cats sleep")
	if n := hits(t, dir, "cat ", "en"); n != 0 {
		t.Fatalf("precondition: %d hits before any index exists", n)
	}
	if err := run(context.Background(), []string{"reindex", "--data", dir, "--languages", "ru, EN"}, noEnv, nil); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, dir, "search_languages"); got != "en,ru" {
		t.Errorf("search_languages = %q, want en,ru", got)
	}
	if got := setting(t, dir, "search_index_signature"); got != "v1:en,ru" {
		t.Errorf("index signature = %q, want v1:en,ru", got)
	}
	if n := hits(t, dir, "собака ", "en", "ru"); n != 1 {
		t.Errorf("Russian word form: %d hits, want 1", n)
	}
	if n := hits(t, dir, "cat ", "en", "ru"); n != 1 {
		t.Errorf("English word form: %d hits, want 1", n)
	}

	// Without --languages the list is kept.
	if err := run(context.Background(), []string{"reindex", "--data", dir}, noEnv, nil); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, dir, "search_languages"); got != "en,ru" {
		t.Errorf("search_languages after a plain reindex = %q", got)
	}
}

func TestReindexCommandRejectsUnknownLanguage(t *testing.T) {
	dir := seedArchive(t, "The cats sleep")
	err := run(context.Background(), []string{"reindex", "--data", dir, "--languages", "en,xx"}, noEnv, nil)
	if err == nil || !strings.Contains(err.Error(), `"xx"`) {
		t.Fatalf("error = %v, want one naming xx", err)
	}
	if got := setting(t, dir, "search_languages"); got != "" {
		t.Errorf("a rejected list was saved: %q", got)
	}
}

// Review Focus 3: a server that finds a stale index rebuilds it.
func TestServeRebuildsAStaleIndexAtStartup(t *testing.T) {
	dir := seedArchive(t, "The cats sleep")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--listen", "127.0.0.1:0", "--data", dir}, noEnv, nil)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for setting(t, dir, "search_index_signature") != "v1:en" {
		select {
		case err := <-done:
			t.Fatalf("serve exited early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the index was not rebuilt")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := hits(t, dir, "cat ", "en"); n != 1 {
		t.Errorf("%d hits after the rebuild, want 1", n)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
}

func TestServeRefusesAnUnknownLanguageSetting(t *testing.T) {
	dir := seedArchive(t, "The cats sleep")
	store, err := archive.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting("search_languages", "en,klingon"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	// A server that does not refuse would run forever; the timeout turns that into a failure.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = run(ctx, []string{"serve", "--listen", "127.0.0.1:0", "--data", dir}, noEnv, nil)
	if err == nil || !strings.Contains(err.Error(), "klingon") {
		t.Fatalf("error = %v, want one naming the bad language", err)
	}
}

func TestDemoCommandAndTheInterface(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := run(context.Background(), []string{"demo", "--data", dir}, noEnv, nil); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"demo", "--data", dir}, noEnv, nil); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second demo run error = %v; want it to refuse an archive that has sources", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := make(chan string, 1)
	done := make(chan error, 1)
	env := func(key string) string {
		if key == "ARC5_PASSWORD" {
			return "from-env"
		}
		return ""
	}
	go func() {
		done <- run(ctx, []string{"serve", "--listen", "127.0.0.1:0", "--data", dir}, env, func(a string) { addr <- a })
	}()
	var base string
	select {
	case a := <-addr:
		base = "http://" + a
	case err := <-done:
		t.Fatalf("serve exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve never became ready")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	read := func(resp *http.Response, err error) (int, string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, body := read(client.Get(base + "/")); code != http.StatusOK || !strings.Contains(body, `name="password"`) {
		t.Fatalf("home before login: %d, want the login page", code)
	}
	code, body := read(client.PostForm(base+"/login", url.Values{"password": {"from-env"}}))
	if code != http.StatusOK || !strings.Contains(body, "Dan Whitfield") || !strings.Contains(body, "Everything at once") {
		t.Fatalf("after login: %d; want the demo chats and the picker", code)
	}
	// The demo connector reports itself live a moment after the server starts.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(body, `aria-label="Live"`) {
		if time.Now().After(deadline) {
			t.Fatal("the demo source never showed as live")
		}
		time.Sleep(20 * time.Millisecond)
		_, body = read(client.Get(base + "/s/chats"))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
}
