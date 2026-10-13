package demo

import (
	"slices"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

func TestSeedWritesAMailbox(t *testing.T) {
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := Seed(store); err != nil {
		t.Fatal(err)
	}
	accounts, _ := store.Accounts()
	var box archive.Account
	for _, a := range accounts {
		if a.Connector == MailName {
			box = a
		}
	}
	if box.Label != "Home mail" || box.Address != "me@example.org" {
		t.Fatalf("mailbox = %+v", box)
	}
	titles := func(f archive.ThreadFilter) []string {
		list, err := store.Threads([]int64{box.ID}, f, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, th := range list {
			out = append(out, th.Title)
		}
		return out
	}
	inbox := titles(archive.ThreadFilter{Role: model.FolderInbox})
	want := []string{"Cena el sábado", "Membership renewal reminder", "Monday planning, room 4", "Deposit for Calle Sol 14",
		"Fotos de Valencia", "Отчёт о походе и расходы", "Your tickets for 11 July"}
	if !slices.Equal(inbox, want) {
		t.Errorf("inbox = %q", inbox)
	}
	list, _ := store.Threads([]int64{box.ID}, archive.ThreadFilter{}, 50, 0)
	for _, th := range list {
		switch th.Title {
		case "Monday planning, room 4":
			if th.Messages != 5 || th.Person != "Dan Whitfield" || !th.HasFiles || th.AllDeleted || !th.WithOwner {
				t.Errorf("planning = %+v", th)
			}
		case "Membership renewal reminder":
			if !th.AllDeleted {
				t.Errorf("the club's reminder is not deleted at source: %+v", th)
			}
		case "Your tickets for 11 July":
			// HTML-only: its text came through the converter.
			if !strings.HasPrefix(th.Snippet, "[North Line Rail]\n\nYour tickets for 11 July\n\nTwo adults, departing 07:42.") ||
				strings.Contains(th.Snippet, "e-tickets are attached") || strings.Contains(th.Snippet, "<") {
				t.Errorf("tickets text = %q", th.Snippet)
			}
		}
	}
	if sent := titles(archive.ThreadFilter{Role: model.FolderSent}); len(sent) != 5 {
		t.Errorf("sent = %q", sent)
	}
	folders, _ := store.AccountFolders(box.ID)
	if len(folders) != 4 || !folders[2].Archived || folders[2].Name != "Receipts" || folders[3].Archived {
		t.Errorf("folders = %+v", folders)
	}

	// Every attachment is stored, so the demo's file cards open.
	var pending int
	store.Reader().QueryRow(`SELECT COUNT(*) FROM attachments a JOIN messages m ON m.id = a.message_id JOIN chats c ON c.id = m.chat_id
		WHERE c.account_id = ? AND a.status != 'stored'`, box.ID).Scan(&pending)
	if pending != 0 {
		t.Errorf("%d attachments are not stored", pending)
	}

	reg := connector.NewRegistry()
	RegisterMail(reg)
	if info, ok := reg.Info(MailName); !ok || info.Display != connector.DisplayMail || !info.Hidden {
		t.Errorf("info = %+v", info)
	}
}
