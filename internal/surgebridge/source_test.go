package surgebridge

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleSurgeMac(body string) string {
	config := base64.StdEncoding.EncodeToString([]byte(body))
	return "Hong Kong=socks5,127.0.0.1,22001,username=sb-a,password=foo\n" +
		"surge-bridge engine=external,exec=\"/opt/homebrew/bin/surge-bridge\",local-port=22001,args=\"-config\",args=\"" + config + "\"\n"
}

func TestExtractEngineConfig(t *testing.T) {
	const expected = `{"mode":"rule","listeners":[{"type":"socks","port":22001}]}`
	text, err := ExtractEngineConfig([]byte(sampleSurgeMac(expected)))
	if err != nil { t.Fatal(err) }
	if string(text) != expected { t.Fatalf("got %s; want %s", text, expected) }
	hidden := strings.Replace(sampleSurgeMac(expected), "surge-bridge engine=external", "# surge-bridge engine=external", 1)
	config, err := ExtractEngineConfig([]byte(hidden))
	if err != nil || string(config) != expected {
		t.Fatalf("hidden comment metadata failed: %v, %s", err, config)
	}
	for _, test := range []struct{name, input string}{
		{"no engine", "Other=socks5,127.0.0.1,12345"},
		{"invalid base64", "x=external,exec=\"/opt/homebrew/bin/surge-bridge\",args=\"-config\",args=\"not--b64\""},
		{"duplicate", sampleSurgeMac(expected) + sampleSurgeMac(expected)},
	} {
		t.Run(test.name,func(t *testing.T){
			if _, err := ExtractEngineConfig([]byte(test.input)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestFetchEngineConfig(t *testing.T) {
	const expected = `{"mode":"rule"}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("target") != "SurgeMac" {
			t.Errorf("expected SurgeMac output")
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Surge") {
			t.Error("missing user-agent")
		}
		w.Write([]byte(sampleSurgeMac(expected)))
	}))
	defer s.Close()
	data, err := FetchEngineConfig(context.Background(), s.Client(), s.URL+"/download/collection")
	if err != nil { t.Fatal(err) }
	if string(data) != expected { t.Fatalf("unexpected config %s", data) }
}

func TestLastGoodPreservedOnInvalidUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private", "last-good.json")
	if err := WriteLastGood(path, []byte(`{"ok":true}`)); err != nil { t.Fatal(err) }
	info, err := os.Stat(path)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0600 {
		t.Fatalf("cache mode = %v; want 0600", info.Mode().Perm())
	}
	if err := WriteLastGood(path, []byte("{invalid")); err == nil {
		t.Fatal("expected invalid configuration to be rejected")
	}
	result, err := ReadLastGood(path)
	if err != nil { t.Fatal(err) }
	if string(result) != `{"ok":true}` { t.Fatalf("cache corrupted: %s", result) }
}
