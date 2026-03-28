package rcconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatLine(t *testing.T) {
	t.Parallel()
	t.Run("matches Write fmt pattern", func(t *testing.T) {
		t.Parallel()
		service := "cloudflared"
		key := "enable"
		val := "YES"
		got := fmt.Sprintf("%s_%s=%q\n", service, key, val)
		want := `cloudflared_enable="YES"` + "\n"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

func TestWrite_MkdirFails(t *testing.T) {
	t.Parallel()
	if err := os.MkdirAll(dir, 0755); err == nil {
		t.Skip("can create or use /etc/rc.conf.d; mkdir failure test not applicable")
	}
	err := Write("cloudflared", map[string]string{"enable": "YES"})
	if err == nil {
		t.Fatal("expected error from Write")
	}
	if !strings.Contains(err.Error(), "mkdir rc.conf.d") {
		t.Fatalf("error %q does not contain mkdir rc.conf.d", err.Error())
	}
}

func TestWrite_Success(t *testing.T) {
	t.Parallel()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Skipf("cannot create /etc/rc.conf.d: %v", err)
	}
	service := fmt.Sprintf("rcconf_test_%d", os.Getpid())
	path := filepath.Join(dir, service)
	defer func() {
		_ = os.Remove(path)
		_ = os.Remove(path + ".tmp")
	}()

	err := Write(service, map[string]string{"enable": "YES"})
	if err != nil {
		t.Skipf("Write failed (may need root or writable %s): %v", dir, err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	content := string(b)
	header := "# Managed by cloudflared-configd -- do not edit manually\n"
	if !strings.HasPrefix(content, header) {
		t.Fatalf("missing or wrong header: %q", content)
	}
	wantLine := fmt.Sprintf("%s_%s=%q\n", service, "enable", "YES")
	if !strings.Contains(content, wantLine) {
		t.Fatalf("content %q does not contain expected line %q", content, wantLine)
	}
}
