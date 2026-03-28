// Package opnsense reads Cloudflared settings from OPNsense config.xml.
// It is the single authoritative source for config.xml parsing used by
// the configd binary.  Nothing else should open config.xml directly.
package opnsense

import (
	"encoding/xml"
	"fmt"
	"os"
)

const DefaultConfigPath = "/conf/config.xml"

// Settings mirrors the <OPNsense><cloudflared> section of config.xml.
type Settings struct {
	Enabled       bool
	Mode          string // "token" | "config"
	Token         string
	TunnelName    string
	PostQuantum   bool
	EdgeIPVersion string // "auto" | "4" | "6"
	Protocol      string // "auto" | "quic" | "http2"
	LogLevel      string // "debug" | "info" | "warn" | "error" | "fatal"
	Tunnels       []Tunnel
}

// Tunnel is a single ingress rule stored under <tunnels><tunnel>.
type Tunnel struct {
	UUID     string
	Enabled  bool
	Hostname string
	Service  string // "http" | "https" | "tcp" | "ssh" | "rdp"
	URL      string
}

// xmlRoot is used only to navigate to the cloudflared section.
type xmlRoot struct {
	OPNsense struct {
		Cloudflared xmlCloudflared `xml:"cloudflared"`
	} `xml:"OPNsense"`
}

type xmlCloudflared struct {
	General xmlGeneral `xml:"general"`
	Tunnels struct {
		Tunnel []xmlTunnel `xml:"tunnel"`
	} `xml:"tunnels"`
}

type xmlGeneral struct {
	Enabled       string `xml:"enabled"`
	Mode          string `xml:"mode"`
	Token         string `xml:"token"`
	TunnelName    string `xml:"tunnel_name"`
	PostQuantum   string `xml:"post_quantum"`
	EdgeIPVersion string `xml:"edge_ip_version"`
	Protocol      string `xml:"protocol"`
	LogLevel      string `xml:"loglevel"`
}

type xmlTunnel struct {
	UUID     string `xml:"uuid,attr"`
	Enabled  string `xml:"enabled"`
	Hostname string `xml:"hostname"`
	Service  string `xml:"service"`
	URL      string `xml:"url"`
}

func boolField(s, dflt string) bool {
	if s == "" {
		s = dflt
	}
	return s == "1" || s == "true" || s == "yes"
}

func strField(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

// ReadSettings parses config.xml at the given path and returns typed settings.
// Missing or empty fields fall back to sensible defaults that match Settings.xml.
func ReadSettings(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config.xml: %w", err)
	}

	var root xmlRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse config.xml: %w", err)
	}

	g := root.OPNsense.Cloudflared.General

	s := &Settings{
		Enabled:       boolField(g.Enabled, "0"),
		Mode:          strField(g.Mode, "token"),
		Token:         g.Token,
		TunnelName:    g.TunnelName,
		PostQuantum:   boolField(g.PostQuantum, "1"),
		EdgeIPVersion: strField(g.EdgeIPVersion, "auto"),
		Protocol:      strField(g.Protocol, "auto"),
		LogLevel:      strField(g.LogLevel, "info"),
	}

	for _, t := range root.OPNsense.Cloudflared.Tunnels.Tunnel {
		s.Tunnels = append(s.Tunnels, Tunnel{
			UUID:     t.UUID,
			Enabled:  boolField(t.Enabled, "1"),
			Hostname: t.Hostname,
			Service:  strField(t.Service, "http"),
			URL:      t.URL,
		})
	}

	return s, nil
}
