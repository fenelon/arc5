package model

import "testing"

func TestArchivedByDefault(t *testing.T) {
	want := map[ChatKind]bool{ChatPrivate: true, ChatGroup: true, ChatChannel: false, ChatBot: false}
	for kind, archived := range want {
		if got := kind.ArchivedByDefault(); got != archived {
			t.Errorf("%s.ArchivedByDefault() = %v, want %v", kind, got, archived)
		}
		if !kind.Valid() {
			t.Errorf("%s.Valid() = false", kind)
		}
	}
	if ChatKind("dm").Valid() {
		t.Error(`ChatKind("dm").Valid() = true`)
	}
}
