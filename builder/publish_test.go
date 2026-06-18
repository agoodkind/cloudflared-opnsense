package main

import (
	"encoding/json"
	"testing"
)

func parseManifest(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

func normalizedFingerprint(t *testing.T, manifest map[string]json.RawMessage, kind pkgKind) string {
	t.Helper()
	if err := normalizeManifest(manifest, kind); err != nil {
		t.Fatalf("normalizeManifest: %v", err)
	}
	fp, err := fingerprintManifest(manifest)
	if err != nil {
		t.Fatalf("fingerprintManifest: %v", err)
	}
	return fp
}

// The binary package's identity is its installed binary hash; the upstream
// version string must not affect the fingerprint, so a rebuild of the same
// upstream version compares equal even when the version field differs.
func TestBinaryFingerprintIgnoresVersion(t *testing.T) {
	t.Parallel()
	a := parseManifest(t, `{"name":"cloudflared","version":"2026.6.0","files":{"/usr/local/bin/cloudflared":"1$aaaa"}}`)
	b := parseManifest(t, `{"name":"cloudflared","version":"2026.7.0","files":{"/usr/local/bin/cloudflared":"1$aaaa"}}`)
	if normalizedFingerprint(t, a, binaryPkg) != normalizedFingerprint(t, b, binaryPkg) {
		t.Fatal("binary fingerprint changed despite identical installed binary")
	}
}

func TestBinaryFingerprintDetectsBinaryChange(t *testing.T) {
	t.Parallel()
	a := parseManifest(t, `{"name":"cloudflared","version":"2026.6.0","files":{"/usr/local/bin/cloudflared":"1$aaaa"}}`)
	b := parseManifest(t, `{"name":"cloudflared","version":"2026.6.0","files":{"/usr/local/bin/cloudflared":"1$bbbb"}}`)
	if normalizedFingerprint(t, a, binaryPkg) == normalizedFingerprint(t, b, binaryPkg) {
		t.Fatal("binary fingerprint unchanged despite a different installed binary hash")
	}
}

// The plugin package sheds revision identity: version, the product annotations,
// and the version-stamp file. Two revisions with identical installed content
// must compare equal.
func TestPluginFingerprintIgnoresRevisionIdentity(t *testing.T) {
	t.Parallel()
	r29 := parseManifest(t, `{
		"version":"2026.6.0_29",
		"annotations":{"product_version":"2026.6.0_29","product_hash":"h29","repository":"cloudflared"},
		"files":{"/usr/local/opnsense/version/cloudflared":"1$v29","/usr/local/bin/cloudflared-configd":"1$same"}
	}`)
	r30 := parseManifest(t, `{
		"version":"2026.6.0_30",
		"annotations":{"product_version":"2026.6.0_30","product_hash":"h30","repository":"cloudflared"},
		"files":{"/usr/local/opnsense/version/cloudflared":"1$v30","/usr/local/bin/cloudflared-configd":"1$same"}
	}`)
	if normalizedFingerprint(t, r29, pluginPkg) != normalizedFingerprint(t, r30, pluginPkg) {
		t.Fatal("plugin fingerprint changed despite identical installed content")
	}
}

func TestPluginFingerprintDetectsConfigdChange(t *testing.T) {
	t.Parallel()
	base := parseManifest(t, `{
		"version":"2026.6.0_29",
		"annotations":{"product_version":"2026.6.0_29","product_hash":"h29"},
		"files":{"/usr/local/opnsense/version/cloudflared":"1$v29","/usr/local/bin/cloudflared-configd":"1$same"}
	}`)
	changed := parseManifest(t, `{
		"version":"2026.6.0_30",
		"annotations":{"product_version":"2026.6.0_30","product_hash":"h30"},
		"files":{"/usr/local/opnsense/version/cloudflared":"1$v30","/usr/local/bin/cloudflared-configd":"1$different"}
	}`)
	if normalizedFingerprint(t, base, pluginPkg) == normalizedFingerprint(t, changed, pluginPkg) {
		t.Fatal("plugin fingerprint unchanged despite a different configd hash")
	}
}

// A real annotation that is not revision identity (here repository) must still
// influence the plugin fingerprint.
func TestPluginFingerprintKeepsRealAnnotations(t *testing.T) {
	t.Parallel()
	a := parseManifest(t, `{"version":"2026.6.0_29","annotations":{"product_hash":"h","repository":"one"},"files":{"/x":"1$same"}}`)
	b := parseManifest(t, `{"version":"2026.6.0_29","annotations":{"product_hash":"h","repository":"two"},"files":{"/x":"1$same"}}`)
	if normalizedFingerprint(t, a, pluginPkg) == normalizedFingerprint(t, b, pluginPkg) {
		t.Fatal("plugin fingerprint ignored a non-identity annotation change")
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	t.Parallel()
	const doc = `{"version":"2026.6.0","files":{"/b":"1$2","/a":"1$1"}}`
	first := normalizedFingerprint(t, parseManifest(t, doc), binaryPkg)
	second := normalizedFingerprint(t, parseManifest(t, doc), binaryPkg)
	if first != second {
		t.Fatalf("fingerprint not deterministic: %s != %s", first, second)
	}
}
