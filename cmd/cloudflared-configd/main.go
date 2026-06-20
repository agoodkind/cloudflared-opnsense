// cloudflared-configd is the configd helper that runs on OPNsense FreeBSD.
// It replaces reconfigure.sh, generate_config.py, and is_enabled.py.
//
// Subcommands (called by actions_cloudflared.conf):
//
//	reconfigure  Read config.xml, write rc.conf.d/cloudflared, write
//	             token/config.yml as needed, start/stop/restart service.
//	status       Exec service cloudflared status.
//	version      Print installed cloudflared binary version.
//	is-enabled   Exit 0 if plugin is enabled, 1 otherwise.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/agoodkind/cloudflared-opnsense/internal/opnsense"
	"github.com/agoodkind/cloudflared-opnsense/internal/rcconf"
)

const (
	defaultConfigDir = "/usr/local/etc/cloudflared"
	rcService        = "cloudflared"
)

type runtimePaths struct {
	configDir       string
	tokenFile       string
	configFile      string
	credentialsFile string
}

type rcConfWriter func(service string, vars map[string]string) error

type serviceManager func(enabled bool) error

type tunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelSecret string `json:"TunnelSecret"`
	TunnelID     string `json:"TunnelID"`
}

var defaultRuntimePaths = runtimePaths{
	configDir:       defaultConfigDir,
	tokenFile:       defaultConfigDir + "/token",
	configFile:      defaultConfigDir + "/config.yml",
	credentialsFile: defaultConfigDir + "/credentials.json",
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error

	switch os.Args[1] {
	case "reconfigure":
		err = cmdReconfigure()
	case "status":
		err = cmdStatus()
	case "version":
		err = cmdVersion()
	case "is-enabled":
		err = cmdIsEnabled()
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "cloudflared-configd %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr,
		"usage: cloudflared-configd <reconfigure|status|version|is-enabled>")
}

// cmdReconfigure is the main entry point called by configd on any settings change.
func cmdReconfigure() error {
	s, err := opnsense.ReadSettings(opnsense.DefaultConfigPath)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	return applySettings(s, defaultRuntimePaths, rcconf.Write, manageService)
}

func applySettings(
	s *opnsense.Settings,
	paths runtimePaths,
	writeRC rcConfWriter,
	manage serviceManager,
) error {
	if s.Enabled && s.Mode == "config" {
		if err := validateTunnelCredentials(s); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(paths.configDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", paths.configDir, err)
	}

	enableVal := "NO"
	if s.Enabled {
		enableVal = "YES"
	}

	pqVal := "NO"
	if s.PostQuantum {
		pqVal = "YES"
	}

	edgeIP := ""
	switch s.EdgeIPVersion {
	case "4", "ipv4":
		edgeIP = "4"
	case "6", "ipv6":
		edgeIP = "6"
	}

	proto := ""
	switch s.Protocol {
	case "quic", "http2":
		proto = s.Protocol
	}

	if err := writeRC(rcService, map[string]string{
		"enable":          enableVal,
		"mode":            s.Mode,
		"token_file":      paths.tokenFile,
		"config":          paths.configFile,
		"loglevel":        s.LogLevel,
		"post_quantum":    pqVal,
		"edge_ip_version": edgeIP,
		"protocol":        proto,
	}); err != nil {
		return fmt.Errorf("write rc.conf.d: %w", err)
	}

	switch s.Mode {
	case "token":
		if err := writeSecret(paths.tokenFile, s.Token); err != nil {
			return fmt.Errorf("write token: %w", err)
		}

	case "config":
		if !hasTunnelCredentials(s) {
			break
		}

		credentials, err := renderCredentialsJSON(s)
		if err != nil {
			return fmt.Errorf("render credentials.json: %w", err)
		}
		if err := writeSecret(paths.credentialsFile, credentials); err != nil {
			return fmt.Errorf("write credentials.json: %w", err)
		}

		yaml, err := renderConfigYAML(s, paths.credentialsFile)
		if err != nil {
			return fmt.Errorf("render config.yml: %w", err)
		}
		if err := writeSecret(paths.configFile, yaml); err != nil {
			return fmt.Errorf("write config.yml: %w", err)
		}
	}

	return manage(s.Enabled)
}

// renderConfigYAML produces a minimal cloudflared config.yml for "config" mode.
// No external YAML library is used; cloudflared config.yml is simple enough
// that hand-rolled output is reliable and keeps the binary dependency-free.
func renderConfigYAML(s *opnsense.Settings, credentialsFile string) (string, error) {
	tunnelID := strings.TrimSpace(s.TunnelID)
	if tunnelID == "" {
		return "", fmt.Errorf("tunnel_id is required in Config File mode")
	}

	out := fmt.Sprintf("tunnel: %s\ncredentials-file: %s\n\ningress:\n",
		tunnelID, credentialsFile)

	for _, t := range s.Tunnels {
		if !t.Enabled || t.Hostname == "" || t.URL == "" {
			continue
		}
		out += fmt.Sprintf("  - hostname: %s\n    service: %s\n", t.Hostname, t.URL)
		if t.Service == "http" || t.Service == "https" {
			noTLS := "false"
			if len(t.URL) > 7 && t.URL[:7] == "http://" {
				noTLS = "true"
			}
			out += fmt.Sprintf("    originRequest:\n      noTLSVerify: %s\n", noTLS)
		}
	}

	out += "  - service: http_status:404\n"
	return out, nil
}

func renderCredentialsJSON(s *opnsense.Settings) (string, error) {
	if err := validateTunnelCredentials(s); err != nil {
		return "", err
	}

	credentials := tunnelCredentials{
		AccountTag:   strings.TrimSpace(s.AccountTag),
		TunnelSecret: strings.TrimSpace(s.TunnelSecret),
		TunnelID:     strings.TrimSpace(s.TunnelID),
	}
	data, err := json.Marshal(credentials)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func hasTunnelCredentials(s *opnsense.Settings) bool {
	return strings.TrimSpace(s.AccountTag) != "" &&
		strings.TrimSpace(s.TunnelID) != "" &&
		strings.TrimSpace(s.TunnelSecret) != ""
}

func validateTunnelCredentials(s *opnsense.Settings) error {
	if strings.TrimSpace(s.AccountTag) == "" {
		return fmt.Errorf("account_tag is required in Config File mode")
	}
	if strings.TrimSpace(s.TunnelID) == "" {
		return fmt.Errorf("tunnel_id is required in Config File mode")
	}
	if strings.TrimSpace(s.TunnelSecret) == "" {
		return fmt.Errorf("tunnel_secret is required in Config File mode")
	}
	return nil
}

func writeSecret(path, content string) error {
	if content == "" {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func manageService(enabled bool) error {
	if enabled {
		if serviceRunning() {
			return runRC("restart")
		}
		return runRC("start")
	}
	_ = runRC("stop")
	return nil
}

func serviceRunning() bool {
	return exec.Command("/usr/sbin/service", rcService, "status").Run() == nil
}

func runRC(action string) error {
	cmd := exec.Command("/usr/sbin/service", rcService, action)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdStatus() error {
	return runRC("status")
}

func cmdVersion() error {
	out, err := exec.Command("/usr/local/bin/cloudflared", "--version").Output()
	if err != nil {
		fmt.Println("not installed")
		return nil
	}
	fmt.Print(string(out))
	return nil
}

func cmdIsEnabled() error {
	s, err := opnsense.ReadSettings(opnsense.DefaultConfigPath)
	if err != nil {
		return err
	}
	if !s.Enabled {
		os.Exit(1)
	}
	return nil
}
