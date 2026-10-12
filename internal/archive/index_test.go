package archive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/fenelon/arc5/internal/model"
)

// wordAnalyzer is a stand-in for the real analyzer: lowercase words in the
// exact column, the same words without a trailing "s" in the stem column.
type wordAnalyzer struct {
	sig string
	tag string // added to every document, so a test can tell which analyzer built the index
}

func (a wordAnalyzer) Index(text string) (string, string) {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	stems := make([]string, len(words))
	for i, w := range words {
		stems[i] = strings.TrimSuffix(w, "s")
	}
	return strings.Join(append(words, a.tag), " "), strings.Join(stems, " ")
}

func (a wordAnalyzer) Signature() string { return a.sig }

// matches returns the source IDs of the messages an FTS5 expression finds.
func matches(t *testing.T, s *Store, expr string) []string {
	t.Helper()
	rows, err := s.Reader().Query(`
		SELECT m.source_id FROM messages_fts JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ? ORDER BY m.id`, expr)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func indexed(t *testing.T) (*Store, int64) {
	t.Helper()
	s := openTemp(t)
	s.SetAnalyzer(wordAnalyzer{sig: "words-1"})
	return s, seedChat(t, s)
}

func TestMessageIsIndexedWhenStored(t *testing.T) {
	s, acc := indexed(t)
	mustUpsert(t, s, acc, textMsg("1", "Hello cats"))
	mustUpsert(t, s, acc, textMsg("2", "Hello dog"))

	if got := matches(t, s, `exact:"cats"`); !slices.Equal(got, []string{"1"}) {
		t.Errorf("exact cats = %v", got)
	}
	if got := matches(t, s, `stem:"cat"`); !slices.Equal(got, []string{"1"}) {
		t.Errorf("stem cat = %v", got)
	}
	if got := matches(t, s, `exact:"hello"`); !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("exact hello = %v", got)
	}
}

// Review Focus 5: an edit is found by its new text and not its old.
func TestEditReplacesTheIndexEntry(t *testing.T) {
	s, acc := indexed(t)
	mustUpsert(t, s, acc, textMsg("1", "Hello cats"))
	edit := textMsg("1", "Hello dogs")
	edit.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, edit)

	if got := matches(t, s, `exact:"cats"`); len(got) != 0 {
		t.Errorf("old text still matches: %v", got)
	}
	if got := matches(t, s, `exact:"dogs"`); !slices.Equal(got, []string{"1"}) {
		t.Errorf("new text = %v", got)
	}
}

func TestDeletedMessageStaysIndexed(t *testing.T) {
	s, acc := indexed(t)
	mustUpsert(t, s, acc, textMsg("1", "Hello cats"))
	if err := s.MarkDeleted(acc, "c1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, s, `exact:"cats"`); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("deleted message = %v", got)
	}
}

func TestAttachmentFileNamesAreIndexed(t *testing.T) {
	s, acc := indexed(t)
	m := textMsg("1", "see attached")
	m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "Quarterly_Report.pdf", Size: 5}}
	mustUpsert(t, s, acc, m)
	for _, word := range []string{"quarterly", "report", "pdf", "attached"} {
		if got := matches(t, s, `exact:"`+word+`"`); !slices.Equal(got, []string{"1"}) {
			t.Errorf("%s = %v", word, got)
		}
	}
}

func TestAttachmentAddedOnReplayIsIndexed(t *testing.T) {
	s, acc := indexed(t)
	mustUpsert(t, s, acc, textMsg("1", "see attached"))
	again := textMsg("1", "see attached")
	again.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "late.pdf", Size: 5}}
	mustUpsert(t, s, acc, again)
	if got := matches(t, s, `exact:"late"`); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("late = %v", got)
	}
}

func TestWithoutAnalyzerMessagesAreStoredButNotIndexed(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "Hello cats"))
	if got := matches(t, s, `exact:"cats"`); len(got) != 0 {
		t.Errorf("indexed without an analyzer: %v", got)
	}
	if current, err := s.IndexCurrent(); err != nil || current {
		t.Errorf("IndexCurrent = %v, %v; want false", current, err)
	}
	if err := s.Reindex(context.Background(), nil); !errors.Is(err, ErrNoAnalyzer) {
		t.Errorf("Reindex error = %v, want ErrNoAnalyzer", err)
	}
}

func TestReindexBuildsTheIndexInBatches(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	for i := range 5 {
		mustUpsert(t, s, acc, textMsg(fmt.Sprint(i), "Hello cats"))
	}
	old := reindexBatch
	reindexBatch = 2
	t.Cleanup(func() { reindexBatch = old })

	s.SetAnalyzer(wordAnalyzer{sig: "words-1"})
	if current, _ := s.IndexCurrent(); current {
		t.Fatal("index reported current before it was built")
	}
	var progress [][2]int
	if err := s.Reindex(context.Background(), func(done, total int) { progress = append(progress, [2]int{done, total}) }); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, s, `stem:"cat"`); len(got) != 5 {
		t.Errorf("matches after reindex = %v", got)
	}
	if want := [][2]int{{2, 5}, {4, 5}, {5, 5}}; !slices.Equal(progress, want) {
		t.Errorf("progress = %v, want %v", progress, want)
	}
	if current, err := s.IndexCurrent(); err != nil || !current {
		t.Errorf("IndexCurrent after reindex = %v, %v", current, err)
	}
}

// Review Focus 3: a changed analyzer makes the index stale, and a rebuild replaces it.
func TestChangedAnalyzerMakesTheIndexStale(t *testing.T) {
	s, acc := indexed(t)
	mustUpsert(t, s, acc, textMsg("1", "Hello cats"))
	if err := s.Reindex(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	s.SetAnalyzer(wordAnalyzer{sig: "words-2", tag: "second"})
	if current, _ := s.IndexCurrent(); current {
		t.Fatal("index reported current under a different analyzer")
	}
	if got := matches(t, s, `exact:"second"`); len(got) != 0 {
		t.Fatalf("new analyzer's tag found before the rebuild: %v", got)
	}
	if err := s.Reindex(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, s, `exact:"second"`); !slices.Equal(got, []string{"1"}) {
		t.Errorf("after the rebuild = %v", got)
	}
	if current, _ := s.IndexCurrent(); !current {
		t.Error("index not current after the rebuild")
	}
}

// Review Focus 3: an interrupted rebuild must not leave the index looking current.
func TestInterruptedReindexLeavesTheIndexStale(t *testing.T) {
	s, acc := indexed(t)
	for i := range 5 {
		mustUpsert(t, s, acc, textMsg(fmt.Sprint(i), "Hello cats"))
	}
	if err := s.Reindex(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	old := reindexBatch
	reindexBatch = 2
	t.Cleanup(func() { reindexBatch = old })

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Reindex(ctx, func(done, total int) { cancel() }) // stop after the first batch
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reindex error = %v, want context.Canceled", err)
	}
	if current, _ := s.IndexCurrent(); current {
		t.Fatal("index reported current after an interrupted rebuild")
	}
	if err := s.Reindex(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, s, `stem:"cat"`); len(got) != 5 {
		t.Errorf("matches after the full rebuild = %v", got)
	}
}

// Review Focus 4: messages that arrive during a rebuild are searchable afterwards.
func TestMessagesArrivingDuringReindexAreIndexed(t *testing.T) {
	s, acc := indexed(t)
	const n = 40
	for i := range n {
		mustUpsert(t, s, acc, textMsg(fmt.Sprintf("old-%d", i), "old message"))
	}
	old := reindexBatch
	reindexBatch = 3
	t.Cleanup(func() { reindexBatch = old })

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range n {
			if _, err := s.UpsertMessage(acc, textMsg(fmt.Sprintf("new-%d", i), "fresh message")); err != nil {
				t.Error(err)
			}
		}
	}()
	if err := s.Reindex(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if got := matches(t, s, `exact:"old"`); len(got) != n {
		t.Errorf("%d old messages indexed, want %d", len(got), n)
	}
	if got := matches(t, s, `exact:"fresh"`); len(got) != n {
		t.Errorf("%d fresh messages indexed, want %d", len(got), n)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Reader().Exec(`INSERT INTO settings (key, value) VALUES ('x', 'y')`); err == nil {
		t.Fatal("write through Reader succeeded")
	}
}
