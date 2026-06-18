package opnsense

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test_boolField(t *testing.T) {
	t.Run("empty_uses_default_false", func(t *testing.T) {
		if got := boolField("", "0"); got != false {
			t.Fatalf("boolField(\"\", \"0\") = %v, want false", got)
		}
	})
	t.Run("explicit_default_1_when_empty", func(t *testing.T) {
		if got := boolField("", "1"); got != true {
			t.Fatalf("boolField(\"\", \"1\") = %v, want true", got)
		}
	})
	t.Run("one_true", func(t *testing.T) {
		if got := boolField("1", "0"); got != true {
			t.Fatalf("boolField(\"1\", \"0\") = %v, want true", got)
		}
	})
	t.Run("true_string", func(t *testing.T) {
		if got := boolField("true", "0"); got != true {
			t.Fatalf("boolField(\"true\", \"0\") = %v, want true", got)
		}
	})
	t.Run("yes_string", func(t *testing.T) {
		if got := boolField("yes", "0"); got != true {
			t.Fatalf("boolField(\"yes\", \"0\") = %v, want true", got)
		}
	})
	t.Run("zero_false", func(t *testing.T) {
		if got := boolField("0", "1"); got != false {
			t.Fatalf("boolField(\"0\", \"1\") = %v, want false", got)
		}
	})
	t.Run("no_false", func(t *testing.T) {
		if got := boolField("no", "1"); got != false {
			t.Fatalf("boolField(\"no\", \"1\") = %v, want false", got)
		}
	})
}

func Test_strField(t *testing.T) {
	t.Run("empty_returns_default", func(t *testing.T) {
		if got := strField("", "default"); got != "default" {
			t.Fatalf("strField(\"\", \"default\") = %q, want %q", got, "default")
		}
	})
	t.Run("nonempty_returns_value", func(t *testing.T) {
		if got := strField("value", "default"); got != "value" {
			t.Fatalf("strField(\"value\", \"default\") = %q, want %q", got, "value")
		}
	})
	t.Run("empty_default_returns_empty", func(t *testing.T) {
		if got := strField("", ""); got != "" {
			t.Fatalf("strField(\"\", \"\") = %q, want empty", got)
		}
	})
}

func TestReadSettings(t *testing.T) {
	t.Run("file_not_found", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.xml")
		_, err := ReadSettings(path)
		if err == nil {
			t.Fatal("ReadSettings: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "read config.xml") {
			t.Fatalf("error %q does not contain %q", err.Error(), "read config.xml")
		}
	})
	t.Run("invalid_xml", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.xml")
		if err := os.WriteFile(path, []byte("<<<not xml>>>"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ReadSettings(path)
		if err == nil {
			t.Fatal("ReadSettings: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "parse config.xml") {
			t.Fatalf("error %q does not contain %q", err.Error(), "parse config.xml")
		}
	})
	t.Run("minimal_defaults", func(t *testing.T) {
		xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<opnsense>
  <OPNsense>
    <cloudflared>
      <general/>
      <tunnels/>
    </cloudflared>
  </OPNsense>
</opnsense>
`
		path := filepath.Join(t.TempDir(), "config.xml")
		if err := os.WriteFile(path, []byte(xmlDoc), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := ReadSettings(path)
		if err != nil {
			t.Fatalf("ReadSettings: %v", err)
		}
		if s.Enabled != false {
			t.Errorf("Enabled = %v, want false", s.Enabled)
		}
		if s.Mode != "token" {
			t.Errorf("Mode = %q, want %q", s.Mode, "token")
		}
		if s.PostQuantum != true {
			t.Errorf("PostQuantum = %v, want true", s.PostQuantum)
		}
		if s.EdgeIPVersion != "auto" {
			t.Errorf("EdgeIPVersion = %q, want %q", s.EdgeIPVersion, "auto")
		}
		if s.Protocol != "auto" {
			t.Errorf("Protocol = %q, want %q", s.Protocol, "auto")
		}
		if s.LogLevel != "info" {
			t.Errorf("LogLevel = %q, want %q", s.LogLevel, "info")
		}
		if len(s.Tunnels) != 0 {
			t.Errorf("len(Tunnels) = %d, want 0", len(s.Tunnels))
		}
	})
	t.Run("fully_populated_two_tunnels", func(t *testing.T) {
		xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<opnsense>
  <system><hostname>router-test</hostname></system>
  <OPNsense>
    <cloudflared>
      <general>
        <enabled>1</enabled>
        <mode>config</mode>
        <token>my-secret-token</token>
        <tunnel_name>main</tunnel_name>
        <post_quantum>0</post_quantum>
        <edge_ip_version>ipv6</edge_ip_version>
        <protocol>quic</protocol>
        <loglevel>debug</loglevel>
      </general>
      <tunnels>
        <tunnel uuid="abc">
          <enabled>1</enabled>
          <hostname>a.example.com</hostname>
          <service>https</service>
          <url>http://127.0.0.1:8080</url>
        </tunnel>
        <tunnel uuid="def">
          <enabled>0</enabled>
          <hostname>b.example.com</hostname>
          <service>tcp</service>
          <url>localhost:22</url>
        </tunnel>
      </tunnels>
    </cloudflared>
  </OPNsense>
</opnsense>
`
		path := filepath.Join(t.TempDir(), "config.xml")
		if err := os.WriteFile(path, []byte(xmlDoc), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := ReadSettings(path)
		if err != nil {
			t.Fatalf("ReadSettings: %v", err)
		}
		if s.Enabled != true {
			t.Errorf("Enabled = %v, want true", s.Enabled)
		}
		if s.Mode != "config" {
			t.Errorf("Mode = %q, want %q", s.Mode, "config")
		}
		if s.Token != "my-secret-token" {
			t.Errorf("Token = %q, want %q", s.Token, "my-secret-token")
		}
		if s.TunnelName != "main" {
			t.Errorf("TunnelName = %q, want %q", s.TunnelName, "main")
		}
		if s.PostQuantum != false {
			t.Errorf("PostQuantum = %v, want false", s.PostQuantum)
		}
		if s.EdgeIPVersion != "ipv6" {
			t.Errorf("EdgeIPVersion = %q, want %q", s.EdgeIPVersion, "ipv6")
		}
		if s.Protocol != "quic" {
			t.Errorf("Protocol = %q, want %q", s.Protocol, "quic")
		}
		if s.LogLevel != "debug" {
			t.Errorf("LogLevel = %q, want %q", s.LogLevel, "debug")
		}
		if len(s.Tunnels) != 2 {
			t.Fatalf("len(Tunnels) = %d, want 2", len(s.Tunnels))
		}
		if s.Tunnels[0].UUID != "abc" || !s.Tunnels[0].Enabled {
			t.Errorf("tunnel[0] = %+v, want uuid abc enabled true", s.Tunnels[0])
		}
		if s.Tunnels[1].UUID != "def" || s.Tunnels[1].Enabled {
			t.Errorf("tunnel[1] = %+v, want uuid def enabled false", s.Tunnels[1])
		}
	})
	t.Run("tunnel_service_defaults_http", func(t *testing.T) {
		xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<opnsense>
  <OPNsense>
    <cloudflared>
      <general/>
      <tunnels>
        <tunnel uuid="only">
          <enabled>1</enabled>
          <hostname>h.example.com</hostname>
          <url>http://127.0.0.1</url>
        </tunnel>
      </tunnels>
    </cloudflared>
  </OPNsense>
</opnsense>
`
		path := filepath.Join(t.TempDir(), "config.xml")
		if err := os.WriteFile(path, []byte(xmlDoc), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := ReadSettings(path)
		if err != nil {
			t.Fatalf("ReadSettings: %v", err)
		}
		if len(s.Tunnels) != 1 {
			t.Fatalf("len(Tunnels) = %d, want 1", len(s.Tunnels))
		}
		if s.Tunnels[0].Service != "http" {
			t.Errorf("Service = %q, want %q", s.Tunnels[0].Service, "http")
		}
	})
	t.Run("tunnel_enabled_defaults_true_when_empty", func(t *testing.T) {
		xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<opnsense>
  <OPNsense>
    <cloudflared>
      <general/>
      <tunnels>
        <tunnel uuid="e1">
          <hostname>h.example.com</hostname>
          <service>http</service>
          <url>http://127.0.0.1</url>
        </tunnel>
      </tunnels>
    </cloudflared>
  </OPNsense>
</opnsense>
`
		path := filepath.Join(t.TempDir(), "config.xml")
		if err := os.WriteFile(path, []byte(xmlDoc), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := ReadSettings(path)
		if err != nil {
			t.Fatalf("ReadSettings: %v", err)
		}
		if len(s.Tunnels) != 1 {
			t.Fatalf("len(Tunnels) = %d, want 1", len(s.Tunnels))
		}
		if !s.Tunnels[0].Enabled {
			t.Errorf("Enabled = %v, want true", s.Tunnels[0].Enabled)
		}
	})
}
