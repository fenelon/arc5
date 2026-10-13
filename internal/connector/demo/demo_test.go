package demo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

func TestSeedBuildsAnArchiveOnce(t *testing.T) {
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	seeded, err := Seed(store)
	if err != nil || !seeded {
		t.Fatalf("Seed = %v, %v", seeded, err)
	}
	accounts, err := store.Accounts()
	if err != nil || len(accounts) != 2 || accounts[0].Connector != Name || accounts[0].Label != "Personal" {
		t.Fatalf("accounts = %+v, %v", accounts, err)
	}
	chats, err := store.ChatList([]int64{accounts[0].ID}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]archive.ChatSummary{}
	for _, c := range chats {
		titles[c.Title] = c
	}
	for _, want := range []string{"Dan Whitfield", "Marina Sokolova", "Familia: Marina y Pablo", "Походы 2026", "Saved Messages"} {
		if _, ok := titles[want]; !ok {
			t.Errorf("no chat titled %q in %v", want, chats)
		}
	}

	dan := titles["Dan Whitfield"]
	for _, title := range []string{"Dan Whitfield", "Marina Sokolova", "Familia: Marina y Pablo"} {
		if titles[title].AvatarSHA == "" {
			t.Errorf("%s has no picture", title)
		}
	}
	if titles["Походы 2026"].AvatarSHA != "" {
		t.Error("every chat has a picture; the demo should show initials too")
	}
	all, err := store.ChatMessages(dan.ID, archive.Anchor{}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var replacedPlans int
	for _, m := range all.Messages {
		if m.SenderID != 0 && m.SenderAvatarSHA != dan.AvatarSHA {
			t.Errorf("Dan's messages show another picture than his chat: %q", m.SenderAvatarSHA)
		}
		for _, r := range m.Replaced {
			if r.FileName == "floor-plan.png" && r.Status == model.StatusStored && len(m.Attachments) == 1 && m.Attachments[0].FileName == "floor-plan-v2.png" {
				replacedPlans++
			}
		}
	}
	if replacedPlans != 1 {
		t.Errorf("want the floor plan replaced by an edit once, got %d", replacedPlans)
	}
	quarters, err := store.ChatQuarters(dan.ID, time.UTC)
	if err != nil || len(quarters) < 8 {
		t.Errorf("Dan's chat spans %d quarters; the year rail needs several years: %v", len(quarters), err)
	}
	window, err := store.ChatMessages(dan.ID, archive.Anchor{}, 60)
	if err != nil {
		t.Fatal(err)
	}
	var outgoing, edited, deleted, stored, skipped int
	for _, m := range window.Messages {
		if m.Outgoing {
			outgoing++
		}
		if m.Versions > 0 {
			edited++
		}
		if !m.DeletedAt.IsZero() {
			deleted++
		}
		for _, a := range m.Attachments {
			switch a.Status {
			case model.StatusStored:
				stored++
				if _, err := os.Stat(store.MediaPath(a.SHA256)); err != nil {
					t.Errorf("stored attachment has no file: %v", err)
				}
			case model.StatusSkipped:
				skipped++
			}
		}
	}
	if outgoing == 0 || edited == 0 || deleted == 0 || stored == 0 || skipped < 2 {
		t.Errorf("the demo chat lacks variety: outgoing %d, edited %d, deleted %d, stored %d, skipped %d",
			outgoing, edited, deleted, stored, skipped)
	}

	again, err := Seed(store)
	if err != nil || again {
		t.Fatalf("second Seed = %v, %v; want it to leave an archive with accounts alone", again, err)
	}
	if after, _ := store.Accounts(); len(after) != 2 {
		t.Fatalf("%d accounts after a second Seed", len(after))
	}
}

func TestDemoConnectorReportsLiveAndStopsWhenCancelled(t *testing.T) {
	reg := connector.NewRegistry()
	Register(reg)
	info, ok := reg.Info(Name)
	if !ok || info.Display != connector.DisplayChats || info.Title == "" {
		t.Fatalf("info = %+v, %v", info, ok)
	}
	c, _ := reg.New(Name)

	ctx, cancel := context.WithCancel(context.Background())
	sink := &progressSink{reported: make(chan connector.Progress, 1)}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, sink, memState{}, nil) }()
	select {
	case p := <-sink.reported:
		if p.Phase != connector.PhaseLive {
			t.Errorf("reported phase %q, want live", p.Phase)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the demo connector reported nothing")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Run returned nil; a connector must return why it stopped")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the demo connector did not stop")
	}
}

type progressSink struct {
	connector.Sink
	reported chan connector.Progress
}

// RequestedMedia never delivers: nobody asks this sink for files.
func (p *progressSink) RequestedMedia() <-chan model.AttachmentRef { return nil }

func (s *progressSink) ReportProgress(p connector.Progress) { s.reported <- p }

// memState is a connector.State in memory.
type memState map[string][]byte

func (m memState) Get(key string) ([]byte, error)     { return m[key], nil }
func (m memState) Set(key string, value []byte) error { m[key] = value; return nil }

func TestDemoIsHiddenButHasSettingsAndALogin(t *testing.T) {
	reg := connector.NewRegistry()
	Register(reg)
	info, _ := reg.Info(Name)
	if !info.Hidden {
		t.Error("the demo connector is offered as a source to add")
	}
	c, _ := reg.New(Name)
	if len(c.Settings()) == 0 {
		t.Error("the demo connector has no settings to show")
	}
	if _, ok := c.(connector.SignOuter); !ok {
		t.Fatal("the demo connector cannot be signed out")
	}

	state := memState{}
	if err := c.(connector.SignOuter).SignOut(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sink := &progressSink{reported: make(chan connector.Progress, 1)}
	if err := c.Run(ctx, sink, state, nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Fatalf("Run after signing out = %v, want ErrNeedsLogin", err)
	}

	flow := c.Setup(state)
	step, err := flow.Next(ctx, nil)
	if err != nil || step.Done || len(step.Fields) != 1 || step.Fields[0].Name != "code" {
		t.Fatalf("first step = %+v, %v", step, err)
	}
	if _, err := flow.Next(ctx, map[string]string{"code": "99999"}); err == nil {
		t.Fatal("a wrong code was accepted")
	}
	step, err = flow.Next(ctx, map[string]string{"code": " 12345 "})
	if err != nil || !step.Done {
		t.Fatalf("after the right code: %+v, %v", step, err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx, sink, state, nil) }()
	select {
	case p := <-sink.reported:
		if p.Phase != connector.PhaseLive {
			t.Errorf("after logging in again: %+v", p)
		}
	case err := <-done:
		t.Fatalf("Run after logging in again returned %v", err)
	}
	stop()
	<-done
}

// fetchSink records what the demo does with a request to fetch a file.
type fetchSink struct {
	connector.Sink
	requests chan model.AttachmentRef
	statuses chan model.AttachmentStatus
}

func (f *fetchSink) RequestedMedia() <-chan model.AttachmentRef { return f.requests }
func (f *fetchSink) ReportProgress(connector.Progress)          {}
func (f *fetchSink) SetAttachmentStatus(_ model.AttachmentRef, s model.AttachmentStatus, _ string) error {
	f.statuses <- s
	return nil
}

func TestDemoAnswersFetchRequests(t *testing.T) {
	sink := &fetchSink{requests: make(chan model.AttachmentRef, 1), statuses: make(chan model.AttachmentStatus, 1)}
	sink.requests <- model.AttachmentRef{ChatSourceID: "dan", MessageSourceID: "1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go idle{}.Run(ctx, sink, memState{}, nil)
	select {
	case s := <-sink.statuses:
		if s != model.StatusUnavailable {
			t.Errorf("status = %q, want unavailable", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the demo never answered the request")
	}
}
