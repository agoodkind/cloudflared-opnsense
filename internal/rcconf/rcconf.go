// Package rcconf writes /etc/rc.conf.d/<service> files.
// Using per-service rc.conf.d files is the standard FreeBSD 12+ pattern
// and avoids the fragility of line-editing the global rc.conf.
package rcconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	dir           = "/etc/rc.conf.d"
	managedHeader = "# Managed by cloudflared-configd -- do not edit manually"
)

// Write atomically replaces /etc/rc.conf.d/<service> with the provided
// key=value pairs.  Keys are upper-cased with the service prefix prepended,
// e.g. Set("cloudflared", {"enable":"YES"}) writes cloudflared_enable="YES".
func Write(service string, vars map[string]string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir rc.conf.d: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(managedHeader + "\n")
	for k, v := range vars {
		fmt.Fprintf(&sb, "%s_%s=%q\n", service, k, v)
	}

	path := filepath.Join(dir, service)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write rc.conf.d tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename rc.conf.d: %w", err)
	}
	return nil
}
