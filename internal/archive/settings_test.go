package archive

import (
	"bytes"
	"testing"
)

func TestSettings(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.Setting("languages"); err != nil || ok {
		t.Fatalf("unset setting: ok = %v, err = %v", ok, err)
	}
	if err := s.SetSetting("languages", "en"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("languages", "en,ru"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Setting("languages"); err != nil || !ok || v != "en,ru" {
		t.Fatalf("setting = %q, %v, %v", v, ok, err)
	}
}

func TestMediaCapDefaultsTo50MB(t *testing.T) {
	s := openTemp(t)
	if n, err := s.MediaCap(); err != nil || n != 50<<20 {
		t.Fatalf("default cap = %d, %v", n, err)
	}
	if err := s.SetMediaCap(1024); err != nil {
		t.Fatal(err)
	}
	if n, err := s.MediaCap(); err != nil || n != 1024 {
		t.Fatalf("cap = %d, %v", n, err)
	}
}

func TestConnectorStateIsPerAccount(t *testing.T) {
	s := openTemp(t)
	a, b := newAccount(t, s), newAccount(t, s)
	if v, err := s.StateGet(a, "session"); err != nil || v != nil {
		t.Fatalf("unset state = %v, %v; want nil, nil", v, err)
	}
	if err := s.StateSet(a, "session", []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.StateSet(a, "session", []byte{3}); err != nil {
		t.Fatal(err)
	}
	if v, err := s.StateGet(a, "session"); err != nil || !bytes.Equal(v, []byte{3}) {
		t.Fatalf("state = %v, %v", v, err)
	}
	if v, _ := s.StateGet(b, "session"); v != nil {
		t.Fatalf("other account sees state %v", v)
	}
}
