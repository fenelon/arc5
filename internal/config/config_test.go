package config

import "testing"

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want Config
	}{
		{"defaults", nil, nil, Config{Listen: "127.0.0.1:8080", DataDir: "data"}},
		{"environment", nil, map[string]string{"ARC5_LISTEN": "0.0.0.0:9000", "ARC5_DATA": "/srv/arc5"}, Config{Listen: "0.0.0.0:9000", DataDir: "/srv/arc5"}},
		{"flags win over environment", []string{"--listen", ":7000", "--data", "/tmp/x"}, map[string]string{"ARC5_LISTEN": "0.0.0.0:9000", "ARC5_DATA": "/srv/arc5"}, Config{Listen: ":7000", DataDir: "/tmp/x"}},
		{"one flag, one from environment", []string{"--data", "/tmp/x"}, map[string]string{"ARC5_LISTEN": ":9000"}, Config{Listen: ":9000", DataDir: "/tmp/x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args, env(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Parse = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"stray"}, {"--listen"}} {
		if _, err := Parse(args, env(nil)); err == nil {
			t.Errorf("Parse(%v) succeeded", args)
		}
	}
}

func TestParseReindex(t *testing.T) {
	got, err := ParseReindex(nil, env(nil))
	if err != nil || got != (Reindex{DataDir: "data"}) {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	got, err = ParseReindex([]string{"--languages", "en,ru"}, env(map[string]string{"ARC5_DATA": "/srv/arc5"}))
	if err != nil || got != (Reindex{DataDir: "/srv/arc5", Languages: "en,ru"}) {
		t.Fatalf("from environment = %+v, %v", got, err)
	}
	got, err = ParseReindex([]string{"--data", "/tmp/x"}, env(map[string]string{"ARC5_DATA": "/srv/arc5"}))
	if err != nil || got.DataDir != "/tmp/x" {
		t.Fatalf("flag over environment = %+v, %v", got, err)
	}
	for _, args := range [][]string{{"--listen", ":1"}, {"stray"}, {"--languages"}} {
		if _, err := ParseReindex(args, env(nil)); err == nil {
			t.Errorf("ParseReindex(%v) succeeded", args)
		}
	}
}

func TestPasswordComesFromTheEnvironmentOnly(t *testing.T) {
	got, err := Parse(nil, env(map[string]string{"ARC5_PASSWORD": "from-env"}))
	if err != nil || got.Password != "from-env" {
		t.Fatalf("Parse = %+v, %v", got, err)
	}
	if _, err := Parse([]string{"--password", "x"}, env(nil)); err == nil {
		t.Error("a --password flag was accepted; flags show in process listings")
	}
}

func TestParseDemo(t *testing.T) {
	got, err := ParseDemo(nil, env(map[string]string{"ARC5_DATA": "/srv/arc5"}))
	if err != nil || got.DataDir != "/srv/arc5" {
		t.Fatalf("from environment = %+v, %v", got, err)
	}
	got, err = ParseDemo([]string{"--data", "/tmp/x"}, env(nil))
	if err != nil || got.DataDir != "/tmp/x" {
		t.Fatalf("flag = %+v, %v", got, err)
	}
	if _, err := ParseDemo([]string{"stray"}, env(nil)); err == nil {
		t.Error("a stray argument was accepted")
	}
}
