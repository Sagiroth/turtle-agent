package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // WriteFile honours the umask
		t.Fatal(err)
	}
	return p
}

const good = `{"realmd":"127.0.0.1:3724","accounts":{"Harness":"s3cret-pw"},"bots":["Gorm","Lia"],
"test_spell":133,"spot":{"map":0,"x":1.5,"y":2.5,"z":3.5}}`

func TestLoadDefaultsAndLookup(t *testing.T) {
	s, err := Load(writeSettings(t, 0o600, good))
	if err != nil {
		t.Fatal(err)
	}
	if s.RPC != DefaultRPC || s.TestSpell != 133 || s.Spot == nil || s.Spot.Y != 2.5 || len(s.Bots) != 2 {
		t.Fatalf("loaded %v", s)
	}
	if pw, err := s.Password("HARNESS"); err != nil || pw != "s3cret-pw" {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := s.Password("other"); err == nil || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "other") {
		t.Fatalf("missing account: %v", err)
	}
}

func TestRejectsGroupReadableFile(t *testing.T) {
	_, err := Load(writeSettings(t, 0o644, good))
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsBadSettings(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field": `{"realmd":"h:1","accounts":{"a":"b"},"pasword":"x"}`,
		"no port":       `{"realmd":"192.0.2.10","accounts":{"a":"b"}}`,
		"public rpc":    `{"realmd":"h:1","accounts":{"a":"b"},"rpc":"0.0.0.0:8765"}`,
		"no accounts":   `{"realmd":"h:1","accounts":{}}`,
		"empty pw":      `{"realmd":"h:1","accounts":{"a":""}}`,
	} {
		if _, err := Load(writeSettings(t, 0o600, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPasswordsNeverFormatted(t *testing.T) {
	s, err := Load(writeSettings(t, 0o600, good))
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprintf("%v", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s), s.String()} {
		if strings.Contains(out, "s3cret") || strings.Contains(out, "Harness") {
			t.Fatalf("password or account name leaked: %s", out)
		}
	}
}

func TestParseErrorDoesNotEchoContent(t *testing.T) {
	_, err := Load(writeSettings(t, 0o600, `{"realmd":"h:1","accounts":{"a":s3cret-pw}}`))
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("err = %v", err)
	}
}
