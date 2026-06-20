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

const (
	testTunnelID        = "11111111-2222-3333-4444-555555555555"
	testCredentialsFile = "/usr/local/etc/cloudflared/credentials.json"
)

func renderTestConfigYAML(t *testing.T, s *opnsense.Settings) string {
	t.Helper()
	if s.TunnelID == "" {
		s.TunnelID = testTunnelID
	}
	out, err := renderConfigYAML(s, testCredentialsFile)
	if err != nil {
		t.Fatalf("renderConfigYAML: %v", err)
	}
	return out
}

func testRuntimePaths(root string) runtimePaths {
	configDir := filepath.Join(root, "cloudflared")
	return runtimePaths{
		configDir:       configDir,
		tokenFile:       filepath.Join(configDir, "token"),
		configFile:      filepath.Join(configDir, "config.yml"),
		credentialsFile: filepath.Join(configDir, "credentials.json"),
	}
}

func Test_renderConfigYAML(t *testing.T) {
	t.Run("TunnelID drives tunnel line when TunnelName is empty", func(t *testing.T) {
		s := &opnsense.Settings{TunnelID: testTunnelID}
		out := renderTestConfigYAML(t, s)
		if !strings.HasPrefix(out, "tunnel: "+testTunnelID+"\n") {
			t.Fatalf("got prefix:\n%s", out)
		}
	})

	t.Run("TunnelName does not change tunnel line", func(t *testing.T) {
		s := &opnsense.Settings{
			TunnelName: "my-named-tunnel",
			TunnelID:   testTunnelID,
		}
		out := renderTestConfigYAML(t, s)
		if !strings.HasPrefix(out, "tunnel: "+testTunnelID+"\n") {
			t.Fatalf("got prefix:\n%s", out)
		}
	})

	t.Run("header matches tunnel line and credentials-file path", func(t *testing.T) {
		s := &opnsense.Settings{TunnelID: testTunnelID}
		out := renderTestConfigYAML(t, s)
		wantPrefix := "tunnel: " + testTunnelID + "\ncredentials-file: " +
			testCredentialsFile + "\n"
		if !strings.HasPrefix(out, wantPrefix) {
			t.Fatalf("want prefix %q, got:\n%s", wantPrefix, out)
		}
	})

	t.Run("missing TunnelID returns error", func(t *testing.T) {
		s := &opnsense.Settings{}
		_, err := renderConfigYAML(s, testCredentialsFile)
		if err == nil {
			t.Fatal("expected error for missing TunnelID")
		}
		if !strings.Contains(err.Error(), "tunnel_id") {
			t.Fatalf("error %q does not mention tunnel_id", err.Error())
		}
	})

	t.Run("output ends with http_status catch-all", func(t *testing.T) {
		s := &opnsense.Settings{}
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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
		out := renderTestConfigYAML(t, s)
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

func Test_renderCredentialsJSON(t *testing.T) {
	s := &opnsense.Settings{
		AccountTag:   " account-tag-value ",
		TunnelID:     " " + testTunnelID + " ",
		TunnelSecret: " secret-value ",
	}
	out, err := renderCredentialsJSON(s)
	if err != nil {
		t.Fatalf("renderCredentialsJSON: %v", err)
	}

	want := `{"AccountTag":"account-tag-value","TunnelSecret":"secret-value","TunnelID":"` +
		testTunnelID + `"}`
	if out != want {
		t.Fatalf("credentials JSON = %q, want %q", out, want)
	}
}

func Test_applySettingsConfigMode(t *testing.T) {
	t.Run("writes credentials and config before managing enabled service", func(t *testing.T) {
		paths := testRuntimePaths(t.TempDir())
		settings := &opnsense.Settings{
			Enabled:       true,
			Mode:          "config",
			AccountTag:    "account-tag-value",
			TunnelID:      testTunnelID,
			TunnelSecret:  "secret-value",
			PostQuantum:   true,
			EdgeIPVersion: "ipv6",
			Protocol:      "quic",
			LogLevel:      "debug",
			Tunnels: []opnsense.Tunnel{
				{
					Enabled:  true,
					Hostname: "app.example.com",
					Service:  "http",
					URL:      "http://127.0.0.1:8080",
				},
			},
		}

		rcCalled := false
		rcVars := map[string]string{}
		manageCalled := false
		err := applySettings(
			settings,
			paths,
			func(service string, vars map[string]string) error {
				rcCalled = true
				if service != rcService {
					t.Fatalf("service = %q, want %q", service, rcService)
				}
				for key, value := range vars {
					rcVars[key] = value
				}
				return nil
			},
			func(enabled bool) error {
				manageCalled = true
				if !enabled {
					t.Fatal("manage called with enabled=false, want true")
				}
				return nil
			},
		)
		if err != nil {
			t.Fatalf("applySettings: %v", err)
		}
		if !rcCalled {
			t.Fatal("rc writer was not called")
		}
		if !manageCalled {
			t.Fatal("service manager was not called")
		}
		if rcVars["config"] != paths.configFile {
			t.Fatalf("rc config path = %q, want %q", rcVars["config"], paths.configFile)
		}
		if rcVars["token_file"] != paths.tokenFile {
			t.Fatalf("rc token path = %q, want %q", rcVars["token_file"], paths.tokenFile)
		}

		credentialsData, readErr := os.ReadFile(paths.credentialsFile)
		if readErr != nil {
			t.Fatalf("read credentials file: %v", readErr)
		}
		wantCredentials := `{"AccountTag":"account-tag-value","TunnelSecret":"secret-value","TunnelID":"` +
			testTunnelID + `"}`
		if string(credentialsData) != wantCredentials {
			t.Fatalf("credentials file = %q, want %q", credentialsData, wantCredentials)
		}
		credentialsInfo, statErr := os.Stat(paths.credentialsFile)
		if statErr != nil {
			t.Fatalf("stat credentials file: %v", statErr)
		}
		if credentialsInfo.Mode().Perm() != 0o600 {
			t.Fatalf("credentials mode = %o, want 0600", credentialsInfo.Mode().Perm())
		}

		configData, readErr := os.ReadFile(paths.configFile)
		if readErr != nil {
			t.Fatalf("read config file: %v", readErr)
		}
		wantPrefix := "tunnel: " + testTunnelID + "\ncredentials-file: " +
			paths.credentialsFile + "\n"
		if !strings.HasPrefix(string(configData), wantPrefix) {
			t.Fatalf("config prefix does not match %q:\n%s", wantPrefix, configData)
		}
	})

	t.Run("missing enabled credentials fail before side effects", func(t *testing.T) {
		cases := []struct {
			name          string
			accountTag    string
			tunnelID      string
			tunnelSecret  string
			wantSubstring string
		}{
			{
				name:          "missing account tag",
				tunnelID:      testTunnelID,
				tunnelSecret:  "secret-value",
				wantSubstring: "account_tag",
			},
			{
				name:          "missing tunnel id",
				accountTag:    "account-tag-value",
				tunnelSecret:  "secret-value",
				wantSubstring: "tunnel_id",
			},
			{
				name:          "missing tunnel secret",
				accountTag:    "account-tag-value",
				tunnelID:      testTunnelID,
				wantSubstring: "tunnel_secret",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				paths := testRuntimePaths(t.TempDir())
				rcCalled := false
				manageCalled := false
				settings := &opnsense.Settings{
					Enabled:      true,
					Mode:         "config",
					AccountTag:   tc.accountTag,
					TunnelID:     tc.tunnelID,
					TunnelSecret: tc.tunnelSecret,
				}
				err := applySettings(
					settings,
					paths,
					func(string, map[string]string) error {
						rcCalled = true
						return nil
					},
					func(bool) error {
						manageCalled = true
						return nil
					},
				)
				if err == nil {
					t.Fatal("expected missing credentials error")
				}
				if !strings.Contains(err.Error(), tc.wantSubstring) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSubstring)
				}
				if rcCalled {
					t.Fatal("rc writer was called before credential validation failed")
				}
				if manageCalled {
					t.Fatal("service manager was called before credential validation failed")
				}
				if _, statErr := os.Stat(paths.configDir); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("config dir stat err = %v, want not exist", statErr)
				}
			})
		}
	})

	t.Run("disabled config mode can save without credentials", func(t *testing.T) {
		paths := testRuntimePaths(t.TempDir())
		rcCalled := false
		manageCalled := false
		settings := &opnsense.Settings{
			Enabled: false,
			Mode:    "config",
		}
		err := applySettings(
			settings,
			paths,
			func(string, map[string]string) error {
				rcCalled = true
				return nil
			},
			func(enabled bool) error {
				manageCalled = true
				if enabled {
					t.Fatal("manage called with enabled=true, want false")
				}
				return nil
			},
		)
		if err != nil {
			t.Fatalf("applySettings disabled config: %v", err)
		}
		if !rcCalled {
			t.Fatal("rc writer was not called")
		}
		if !manageCalled {
			t.Fatal("service manager was not called")
		}
		if _, statErr := os.Stat(paths.credentialsFile); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("credentials stat err = %v, want not exist", statErr)
		}
		if _, statErr := os.Stat(paths.configFile); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("config stat err = %v, want not exist", statErr)
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
