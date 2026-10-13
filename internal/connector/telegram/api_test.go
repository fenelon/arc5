package telegram

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// Review Focus 1: Telegram starts a self-destruct countdown when a client
// reports media as opened. Nothing in this connector may be able to.
func TestNoReadMessageContents(t *testing.T) {
	for _, iface := range []reflect.Type{reflect.TypeFor[api](), reflect.TypeFor[conn]()} {
		for i := range iface.NumMethod() {
			if name := strings.ToLower(iface.Method(i).Name); strings.Contains(name, "read") || strings.Contains(name, "contents") {
				t.Errorf("%s has a method %s", iface, iface.Method(i).Name)
			}
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal(files, err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"ReadMessageContents", "readMessageContents", "ReadHistory", "ReadMentions"} {
			if bytes.Contains(src, []byte(banned)) {
				t.Errorf("%s mentions %s", f, banned)
			}
		}
	}
}

// fakeInvoker answers Telegram requests from a function, as the server would.
type fakeInvoker struct {
	answer func(req bin.Encoder) (bin.Encoder, error)
	calls  []bin.Encoder
}

func (f *fakeInvoker) Invoke(_ context.Context, in bin.Encoder, out bin.Decoder) error {
	f.calls = append(f.calls, in)
	res, err := f.answer(in)
	if err != nil {
		return err
	}
	var b bin.Buffer
	if err := res.Encode(&b); err != nil {
		return err
	}
	return out.Decode(&b)
}

func TestClientHistory(t *testing.T) {
	inv := &fakeInvoker{answer: func(req bin.Encoder) (bin.Encoder, error) {
		return &tg.MessagesChannelMessages{
			Messages: []tg.MessageClass{&tg.Message{ID: 9, PeerID: &tg.PeerChannel{ChannelID: 20}, Message: "hi"}},
			Users:    []tg.UserClass{&tg.User{ID: 1, FirstName: "Dan"}},
			Chats:    []tg.ChatClass{&tg.Channel{ID: 20, Title: "Походы", Megagroup: true, Photo: &tg.ChatPhotoEmpty{}}},
		}, nil
	}}
	c := newClient(inv, credentials{ID: 1, Hash: "h"})
	peer := &tg.InputPeerChannel{ChannelID: 20, AccessHash: 21}
	page, err := c.History(context.Background(), peer, 500, 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	req, ok := inv.calls[0].(*tg.MessagesGetHistoryRequest)
	if !ok || req.OffsetID != 500 || req.MinID != 100 || req.Limit != 50 || req.Peer != peer {
		t.Errorf("request = %#v", inv.calls[0])
	}
	if len(page.Messages) != 1 || len(page.Users) != 1 || len(page.Chats) != 1 {
		t.Errorf("page = %+v", page)
	}
}

func TestClientMessagesByID(t *testing.T) {
	inv := &fakeInvoker{answer: func(req bin.Encoder) (bin.Encoder, error) {
		return &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 3, PeerID: &tg.PeerUser{UserID: 1}}}}, nil
	}}
	c := newClient(inv, credentials{})
	if _, err := c.Messages(context.Background(), &tg.InputPeerChannel{ChannelID: 20, AccessHash: 21}, []int{3}); err != nil {
		t.Fatal(err)
	}
	if req, ok := inv.calls[0].(*tg.ChannelsGetMessagesRequest); !ok || len(req.ID) != 1 {
		t.Errorf("a channel's messages are asked of the channel: %#v", inv.calls[0])
	}
	page, err := c.Messages(context.Background(), &tg.InputPeerUser{UserID: 1, AccessHash: 11}, []int{3})
	if err != nil || len(page.Messages) != 1 {
		t.Fatal(page, err)
	}
	if _, ok := inv.calls[1].(*tg.MessagesGetMessagesRequest); !ok {
		t.Errorf("a private chat's messages: %#v", inv.calls[1])
	}
}

func TestClientDialogs(t *testing.T) {
	slice := &tg.MessagesDialogsSlice{
		Count: 300,
		Dialogs: []tg.DialogClass{
			&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}, TopMessage: 50},
			&tg.DialogFolder{Peer: &tg.PeerChannel{ChannelID: 99}},
			&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 20}, TopMessage: 70},
		},
		Messages: []tg.MessageClass{
			&tg.Message{ID: 50, PeerID: &tg.PeerUser{UserID: 1}, Date: 2000},
			&tg.Message{ID: 70, PeerID: &tg.PeerChannel{ChannelID: 20}, Date: 1000},
		},
		Users: []tg.UserClass{&tg.User{ID: 1, AccessHash: 11, FirstName: "Dan"}},
		Chats: []tg.ChatClass{&tg.Channel{ID: 20, AccessHash: 21, Title: "Походы", Photo: &tg.ChatPhotoEmpty{}}},
	}
	inv := &fakeInvoker{answer: func(bin.Encoder) (bin.Encoder, error) { return slice, nil }}
	c := newClient(inv, credentials{})
	page, err := c.Dialogs(context.Background(), dialogOffset{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Dialogs) != 2 || page.Next == nil {
		t.Fatalf("page = %+v", page)
	}
	if n := page.Next; n.Date != 1000 || n.ID != 70 || !reflect.DeepEqual(n.Peer, tg.InputPeerClass(&tg.InputPeerChannel{ChannelID: 20, AccessHash: 21})) {
		t.Errorf("next offset = %+v", n)
	}
	if req := inv.calls[0].(*tg.MessagesGetDialogsRequest); req.Limit != dialogsPerPage {
		t.Errorf("limit = %d", req.Limit)
	}
	if _, ok := inv.calls[0].(*tg.MessagesGetDialogsRequest).OffsetPeer.(*tg.InputPeerEmpty); !ok {
		t.Error("the first page starts from no peer")
	}

	all := &tg.MessagesDialogs{Dialogs: slice.Dialogs, Messages: slice.Messages, Users: slice.Users, Chats: slice.Chats}
	inv.answer = func(bin.Encoder) (bin.Encoder, error) { return all, nil }
	if page, err := c.Dialogs(context.Background(), dialogOffset{}); err != nil || page.Next != nil {
		t.Errorf("a complete list has no next page: %+v, %v", page.Next, err)
	}
	short := &tg.MessagesDialogsSlice{Count: 2, Dialogs: slice.Dialogs, Messages: slice.Messages, Users: slice.Users, Chats: slice.Chats}
	inv.answer = func(bin.Encoder) (bin.Encoder, error) { return short, nil }
	if page, err := c.Dialogs(context.Background(), dialogOffset{}); err != nil || page.Next != nil {
		t.Errorf("a slice shorter than a page is the last: %+v, %v", page.Next, err)
	}
}

func TestClientLogin(t *testing.T) {
	inv := &fakeInvoker{answer: func(req bin.Encoder) (bin.Encoder, error) {
		switch r := req.(type) {
		case *tg.AuthSendCodeRequest:
			if r.PhoneNumber != "+44 20" || r.APIID != 7 || r.APIHash != "hash" {
				return nil, errors.New("unexpected send-code request")
			}
			return &tg.AuthSentCode{PhoneCodeHash: "code-hash", Type: &tg.AuthSentCodeTypeApp{Length: 5}}, nil
		case *tg.AuthSignInRequest:
			if r.PhoneCode == "11111" {
				return nil, tgerr.New(401, "SESSION_PASSWORD_NEEDED")
			}
			if r.PhoneCode == "00000" {
				return nil, tgerr.New(400, "PHONE_CODE_INVALID")
			}
			return &tg.AuthAuthorization{User: &tg.User{ID: 1}}, nil
		case *tg.UsersGetUsersRequest:
			return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 1, Self: true, FirstName: "Me"}}}, nil
		}
		return nil, errors.New("unexpected request")
	}}
	c := newClient(inv, credentials{ID: 7, Hash: "hash"})
	ctx := context.Background()
	hash, err := c.SendCode(ctx, "+44 20")
	if err != nil || hash != "code-hash" {
		t.Fatalf("SendCode = %q, %v", hash, err)
	}
	if err := c.SignIn(ctx, "+44 20", "11111", hash); !errors.Is(err, errPasswordNeeded) {
		t.Errorf("a two-step account: %v", err)
	}
	if err := c.SignIn(ctx, "+44 20", "00000", hash); !tgerr.Is(err, "PHONE_CODE_INVALID") {
		t.Errorf("a wrong code: %v", err)
	}
	if err := c.SignIn(ctx, "+44 20", "12345", hash); err != nil {
		t.Errorf("the right code: %v", err)
	}
	if me, err := c.Self(ctx); err != nil || me.ID != 1 {
		t.Errorf("Self = %+v, %v", me, err)
	}
}

func TestClientDownload(t *testing.T) {
	inv := &fakeInvoker{answer: func(req bin.Encoder) (bin.Encoder, error) {
		if _, ok := req.(*tg.UploadGetFileRequest); !ok {
			return nil, errors.New("unexpected request")
		}
		return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte("jpeg bytes")}, nil
	}}
	var buf bytes.Buffer
	if err := newClient(inv, credentials{}).Download(context.Background(), &tg.InputPhotoFileLocation{ID: 1}, &buf); err != nil || buf.String() != "jpeg bytes" {
		t.Errorf("Download = %q, %v", buf.String(), err)
	}
}

func TestErrorKinds(t *testing.T) {
	if d, ok := floodWait(tgerr.New(420, "FLOOD_WAIT_30")); !ok || d.Seconds() != 30 {
		t.Errorf("flood wait = %v, %v", d, ok)
	}
	if _, ok := floodWait(errors.New("other")); ok {
		t.Error("a plain error is a flood wait")
	}
	for _, e := range []error{tgerr.New(401, "AUTH_KEY_UNREGISTERED"), tgerr.New(401, "SESSION_REVOKED"), tgerr.New(401, "USER_DEACTIVATED")} {
		if !unauthorized(e) {
			t.Errorf("%v is not unauthorized", e)
		}
	}
	if unauthorized(tgerr.New(400, "PEER_ID_INVALID")) || unauthorized(errors.New("network")) {
		t.Error("an ordinary error counts as unauthorized")
	}
	for _, e := range []error{tgerr.New(400, "FILE_REFERENCE_EXPIRED"), tgerr.New(400, "FILE_REFERENCE_INVALID")} {
		if !referenceExpired(e) {
			t.Errorf("%v is not an expired reference", e)
		}
	}
	for _, e := range []error{tgerr.New(400, "FILE_ID_INVALID"), tgerr.New(400, "LOCATION_INVALID"), tgerr.New(400, "MEDIA_EMPTY")} {
		if !fileGone(e) {
			t.Errorf("%v is not a file Telegram no longer has", e)
		}
	}
}
