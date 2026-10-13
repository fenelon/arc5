package archive

import (
	"errors"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

func TestPutAvatar(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	chat := model.AvatarRef{Kind: model.AvatarChat, SourceID: "c1", AvatarID: "photo:1"}
	person := model.AvatarRef{Kind: model.AvatarSender, SourceID: "u1", AvatarID: "photo:1"}

	if stored, err := s.AvatarStored(acc, chat); err != nil || stored {
		t.Fatalf("before any picture: %v, %v", stored, err)
	}
	for _, ref := range []model.AvatarRef{chat, person} {
		if err := s.PutAvatar(acc, ref, strings.NewReader("jpeg bytes")); err != nil {
			t.Fatal(err)
		}
		if stored, err := s.AvatarStored(acc, ref); err != nil || !stored {
			t.Errorf("%s after PutAvatar: %v, %v", ref.Kind, stored, err)
		}
	}
	if files := mediaFiles(t, s); len(files) != 1 {
		t.Errorf("the same picture for the chat and its person is kept once, got %d files", len(files))
	}

	info, err := s.Chat(chatDBID(t, s, acc, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.AvatarSHA == "" || s.MediaPath(info.AvatarSHA) != mediaFiles(t, s)[0] {
		t.Errorf("chat avatar = %q", info.AvatarSHA)
	}
	list, err := s.ChatList([]int64{acc}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = list // the chat has no messages yet, so it is not listed

	newer := chat
	newer.AvatarID = "photo:2"
	if stored, _ := s.AvatarStored(acc, newer); stored {
		t.Error("a new picture identity counts as stored")
	}
	if err := s.PutAvatar(acc, newer, strings.NewReader("newer bytes")); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Chat(chatDBID(t, s, acc, "c1")); again.AvatarSHA == info.AvatarSHA || again.AvatarSHA == "" {
		t.Errorf("a later picture did not replace the first: %q", again.AvatarSHA)
	}
	if stored, _ := s.AvatarStored(acc, chat); stored {
		t.Error("the replaced picture's identity still counts as stored")
	}
	if !s.IsAvatar(info.AvatarSHA) {
		t.Error("the person's picture is no longer known as an avatar")
	}

	for name, c := range map[string]struct {
		ref  model.AvatarRef
		want error
	}{
		"unknown chat":   {model.AvatarRef{Kind: model.AvatarChat, SourceID: "nope", AvatarID: "p"}, ErrUnknownChat},
		"unknown sender": {model.AvatarRef{Kind: model.AvatarSender, SourceID: "nope", AvatarID: "p"}, ErrNotFound},
		"no identity":    {model.AvatarRef{Kind: model.AvatarChat, SourceID: "c1"}, ErrInvalid},
		"no kind":        {model.AvatarRef{SourceID: "c1", AvatarID: "p"}, ErrInvalid},
	} {
		if err := s.PutAvatar(acc, c.ref, strings.NewReader("x")); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if s.IsAvatar(strings.Repeat("0", 64)) {
		t.Error("an unknown hash is an avatar")
	}
}

func TestAvatarsReachTheReadSide(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	if err := s.PutAvatar(acc, model.AvatarRef{Kind: model.AvatarChat, SourceID: "c1", AvatarID: "a"}, strings.NewReader("chat")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAvatar(acc, model.AvatarRef{Kind: model.AvatarSender, SourceID: "u1", AvatarID: "b"}, strings.NewReader("person")); err != nil {
		t.Fatal(err)
	}
	list, err := s.ChatList([]int64{acc}, 10, 0)
	if err != nil || len(list) != 1 || list[0].AvatarSHA == "" {
		t.Fatalf("chat list = %+v, %v", list, err)
	}
	w, err := s.ChatMessages(list[0].ID, Anchor{}, 10)
	if err != nil || len(w.Messages) != 1 || w.Messages[0].SenderAvatarSHA == "" || w.Messages[0].SenderAvatarSHA == list[0].AvatarSHA {
		t.Fatalf("messages = %+v, %v", w.Messages, err)
	}
	sender, err := s.Sender(w.Messages[0].SenderID)
	if err != nil || sender.AvatarSHA != w.Messages[0].SenderAvatarSHA {
		t.Errorf("sender = %+v, %v", sender, err)
	}
}
