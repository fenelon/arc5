package archive

import (
	"errors"
	"testing"
)

func TestAccountRoundTrip(t *testing.T) {
	s := openTemp(t)
	id, err := s.CreateAccount("telegram", "Personal", map[string]string{"pace": "slow"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Account(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Connector != "telegram" || got.Label != "Personal" || got.Settings["pace"] != "slow" || !got.Enabled {
		t.Fatalf("account = %+v", got)
	}

	if err := s.UpdateAccountSettings(id, map[string]string{"pace": "fast"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	all, err := s.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Settings["pace"] != "fast" || all[0].Enabled {
		t.Fatalf("accounts = %+v", all)
	}
}

func TestAccountNilSettingsBecomeEmptyMap(t *testing.T) {
	s := openTemp(t)
	id, err := s.CreateAccount("telegram", "Personal", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Account(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings == nil || len(got.Settings) != 0 {
		t.Fatalf("settings = %#v, want empty non-nil map", got.Settings)
	}
}

func TestAccountErrors(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Account(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account(99) error = %v, want ErrNotFound", err)
	}
	if err := s.UpdateAccountSettings(99, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAccountSettings(99) error = %v, want ErrNotFound", err)
	}
	if _, err := s.CreateAccount("", "x", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateAccount with no connector error = %v, want ErrInvalid", err)
	}
}
