package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agoodkind/cloudflared-opnsense/internal/opnsense"
)

func Test_renderConfigYAML(t *testing.T) {
	t.Run("empty TunnelName uses default opnsense-tunnel", func(t *testing.T) {
		s := &opnsense.Settings{TunnelName: ""}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if !strings.HasPrefix(out, "tunnel: opnsense-tunnel\n") {
			t.Fatalf("got prefix:\n%s", out)
		}
	})

	t.Run("non-empty TunnelName is used as-is", func(t *testing.T) {
		s := &opnsense.Settings{TunnelName: "my-named-tunnel"}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if !strings.HasPrefix(out, "tunnel: my-named-tunnel\n") {
			t.Fatalf("got prefix:\n%s", out)
		}
	})

	t.Run("header matches tunnel line and credentials-file path", func(t *testing.T) {
		s := &opnsense.Settings{TunnelName: "t1"}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		wantPrefix := "tunnel: t1\ncredentials-file: /usr/local/etc/cloudflared/cert.pem\n"
		if !strings.HasPrefix(out, wantPrefix) {
			t.Fatalf("want prefix %q, got:\n%s", wantPrefix, out)
		}
	})

	t.Run("output ends with http_status catch-all", func(t *testing.T) {
		s := &opnsense.Settings{}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		wantSuffix := "  - service: http_status:404\n"
		if !strings.HasSuffix(out, wantSuffix) {
			t.Fatalf("want suffix %q, got:\n%s", wantSuffix, out)
		}
	})

	t.Run("disabled tunnel is skipped", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  false,
					Hostname: "a.example.com",
					URL:      "http://127.0.0.1:1",
					Service:  "http",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if strings.Contains(out, "a.example.com") {
			t.Fatalf("disabled tunnel should not appear:\n%s", out)
		}
	})

	t.Run("tunnel with empty Hostname is skipped", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{Enabled: true, Hostname: "", URL: "http://127.0.0.1:1", Service: "http"},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if strings.Contains(out, "127.0.0.1:1") {
			t.Fatalf("empty hostname tunnel should not appear:\n%s", out)
		}
	})

	t.Run("tunnel with empty URL is skipped", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{Enabled: true, Hostname: "b.example.com", URL: "", Service: "http"},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if strings.Contains(out, "b.example.com") {
			t.Fatalf("empty URL tunnel should not appear:\n%s", out)
		}
	})

	t.Run("enabled tunnel with Hostname and URL is included", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "app.example.com",
					URL:      "tcp://127.0.0.1:22",
					Service:  "tcp",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		want := "  - hostname: app.example.com\n    service: tcp://127.0.0.1:22\n"
		if !strings.Contains(out, want) {
			t.Fatalf("want substring %q in:\n%s", want, out)
		}
	})

	t.Run("service http with http URL sets noTLSVerify true", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "h.example.com",
					URL:      "http://127.0.0.1:80",
					Service:  "http",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if !strings.Contains(out, "noTLSVerify: true") {
			t.Fatalf("want noTLSVerify true in:\n%s", out)
		}
	})

	t.Run("service http with https URL sets noTLSVerify false", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "h2.example.com",
					URL:      "https://127.0.0.1:443",
					Service:  "http",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if !strings.Contains(out, "noTLSVerify: false") {
			t.Fatalf("want noTLSVerify false in:\n%s", out)
		}
	})

	t.Run("service https sets originRequest block", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "hs.example.com",
					URL:      "https://127.0.0.1:8443",
					Service:  "https",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		want := "    originRequest:\n      noTLSVerify: false\n"
		if !strings.Contains(out, want) {
			t.Fatalf("want originRequest block %q in:\n%s", want, out)
		}
	})

	t.Run("service tcp does not set originRequest block", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "t.example.com",
					URL:      "tcp://127.0.0.1:22",
					Service:  "tcp",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if strings.Contains(out, "originRequest") {
			t.Fatalf("tcp tunnel should not include originRequest:\n%s", out)
		}
	})

	t.Run("multiple tunnels are all rendered", func(t *testing.T) {
		s := &opnsense.Settings{
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "one.example.com",
					URL:      "http://127.0.0.1:1",
					Service:  "tcp",
				},
				{
					Enabled:  true,
					Hostname: "two.example.com",
					URL:      "http://127.0.0.1:2",
					Service:  "tcp",
				},
			},
		}
		out, err := renderConfigYAML(s)
		if err != nil {
			t.Fatalf("renderConfigYAML: %v", err)
		}
		if !strings.Contains(out, "one.example.com") || !strings.Contains(out, "two.example.com") {
			t.Fatalf("want both hostnames in:\n%s", out)
		}
		idx1 := strings.Index(out, "one.example.com")
		idx2 := strings.Index(out, "two.example.com")
		if idx1 < 0 || idx2 < 0 || idx1 >= idx2 {
			t.Fatalf("want order one then two, got idx1=%d idx2=%d\n%s", idx1, idx2, out)
		}
	})
}

func Test_writeSecret(t *testing.T) {
	t.Run("empty content returns nil without creating any file", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "secret")
		err := writeSecret(path, "")
		if err != nil {
			t.Fatalf("writeSecret empty: %v", err)
		}
		if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("want no file at %s, stat err=%v", path, statErr)
		}
		tmpPath := path + ".tmp"
		if _, statErr := os.Stat(tmpPath); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("want no temp file at %s, stat err=%v", tmpPath, statErr)
		}
	})

	t.Run("non-empty content writes file mode 0600 with correct content", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "secret")
		content := "hello-world\n"
		err := writeSecret(path, content)
		if err != nil {
			t.Fatalf("writeSecret: %v", err)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile: %v", readErr)
		}
		if string(data) != content {
			t.Fatalf("content: got %q want %q", data, content)
		}
		fi, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("Stat: %v", statErr)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode: got %o want 0600", fi.Mode().Perm())
		}
	})

	t.Run("non-empty content ends at exact path not dot tmp", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "secret")
		err := writeSecret(path, "x")
		if err != nil {
			t.Fatalf("writeSecret: %v", err)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("Stat final path: %v", statErr)
		}
		tmpPath := path + ".tmp"
		if _, statErr := os.Stat(tmpPath); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("want no .tmp after rename, stat err=%v", statErr)
		}
	})

	t.Run("write under missing parent directory returns error", func(t *testing.T) {
		base := t.TempDir()
		path := filepath.Join(base, "nope", "secret")
		err := writeSecret(path, "data")
		if err == nil {
			t.Fatal("expected error when parent directory does not exist")
		}
	})
}
