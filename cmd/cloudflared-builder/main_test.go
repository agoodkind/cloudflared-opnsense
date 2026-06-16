package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func Test_emptyOr(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns default", func(t *testing.T) {
		t.Parallel()
		got := emptyOr("", "default")
		if got != "default" {
			t.Fatalf("emptyOr(\"\"): got %q want %q", got, "default")
		}
	})

	t.Run("non-empty string returns value", func(t *testing.T) {
		t.Parallel()
		got := emptyOr("x", "default")
		if got != "x" {
			t.Fatalf("emptyOr(\"x\"): got %q want %q", got, "x")
		}
	})
}

func Test_filterLines(t *testing.T) {
	t.Parallel()

	t.Run("keeps lines matching predicate", func(t *testing.T) {
		t.Parallel()
		got := filterLines("a\nb\nc\n", func(l string) bool { return l == "b" })
		want := "b\n"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("drops lines not matching predicate", func(t *testing.T) {
		t.Parallel()
		got := filterLines("a\nb\n", func(l string) bool { return false })
		want := ""
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("empty string produces empty string", func(t *testing.T) {
		t.Parallel()
		got := filterLines("", func(l string) bool { return false })
		if got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})

	t.Run("all lines kept", func(t *testing.T) {
		t.Parallel()
		got := filterLines("x\ny", func(l string) bool { return true })
		want := "x\ny\n"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("all lines dropped", func(t *testing.T) {
		t.Parallel()
		got := filterLines("x\ny\n", func(l string) bool { return false })
		if got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})
}

func ndjsonLine(obj map[string]any) string {
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func parseNDJSON(t *testing.T, s string) []map[string]any {
	t.Helper()
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse line: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func Test_parseUCLManifest(t *testing.T) {
	t.Parallel()

	ucl := `name: os-cloudflared
version: "2026.3.0_1"
origin: opnsense/os-cloudflared
categories: [ "net" ]
www: "https://goodkind.io"
annotations {
    "product_email": "alex@goodkind.io",
    "product_id": "os-cloudflared",
    "product_name": "cloudflared",
    "product_website": "https://goodkind.io"
}
`

	manifest := parseUCLManifest(ucl)
	if manifest["name"] != "os-cloudflared" {
		t.Fatalf("name = %v", manifest["name"])
	}
	if manifest["www"] != "https://goodkind.io" {
		t.Fatalf("www = %v", manifest["www"])
	}

	categories, ok := manifest["categories"].([]string)
	if !ok {
		t.Fatalf("categories type = %T", manifest["categories"])
	}
	if len(categories) != 1 || categories[0] != "net" {
		t.Fatalf("categories = %v", categories)
	}

	annotations, ok := manifest["annotations"].(map[string]string)
	if !ok {
		t.Fatalf("annotations type = %T", manifest["annotations"])
	}
	if annotations["product_email"] != "alex@goodkind.io" {
		t.Fatalf("product_email = %q", annotations["product_email"])
	}
	if annotations["product_id"] != "os-cloudflared" {
		t.Fatalf("product_id = %q", annotations["product_id"])
	}
	if _, exists := manifest["product_email"]; exists {
		t.Fatalf("product_email should not be top-level: %v", manifest)
	}
}

func TestCreatePkgArchiveUsesLegacyManifestLayout(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stagingDir := filepath.Join(tempDir, "staging")
	binDir := filepath.Join(stagingDir, "usr", "local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "cloudflared"), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}

	plistPath := filepath.Join(tempDir, "plist")
	if err := os.WriteFile(plistPath, []byte("/usr/local/bin/cloudflared\n@dir /usr/local/etc/cloudflared\n"), 0644); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(tempDir, "cloudflared.pkg")
	manifest := strings.Join([]string{
		"name: os-cloudflared",
		"version: \"2026.6.0_1\"",
		"origin: opnsense/os-cloudflared",
		"prefix: /usr/local",
		"categories: [ \"net\" ]",
		"",
	}, "\n")
	scripts := map[string]string{"post-install": "#!/bin/sh\necho ok\n"}

	if err := createPkgArchive(outputPath, stagingDir, plistPath, manifest, "desc\n", scripts); err != nil {
		t.Fatalf("createPkgArchive: %v", err)
	}

	manifestBody, compactManifestBody := readPkgManifestFiles(t, outputPath)

	var full map[string]any
	if err := json.Unmarshal(manifestBody, &full); err != nil {
		t.Fatalf("unmarshal +MANIFEST: %v", err)
	}
	files, ok := full["files"].(map[string]any)
	if !ok {
		t.Fatalf("files type = %T", full["files"])
	}
	hashValue, ok := files["/usr/local/bin/cloudflared"].(string)
	if !ok {
		t.Fatalf("hash value type = %T", files["/usr/local/bin/cloudflared"])
	}
	if !strings.HasPrefix(hashValue, "1$") {
		t.Fatalf("hash value = %q, want legacy 1$ prefix", hashValue)
	}

	directories, ok := full["directories"].(map[string]any)
	if !ok {
		t.Fatalf("directories type = %T", full["directories"])
	}
	if directories["/usr/local/etc/cloudflared"] != "y" {
		t.Fatalf("directory marker = %v, want y", directories["/usr/local/etc/cloudflared"])
	}

	if _, ok := full["scripts"].(map[string]any); !ok {
		t.Fatalf("scripts type = %T", full["scripts"])
	}

	var compact map[string]any
	if err := json.Unmarshal(compactManifestBody, &compact); err != nil {
		t.Fatalf("unmarshal +COMPACT_MANIFEST: %v", err)
	}
	if _, exists := compact["files"]; exists {
		t.Fatalf("compact manifest unexpectedly contains files: %v", compact)
	}
	if _, exists := compact["directories"]; exists {
		t.Fatalf("compact manifest unexpectedly contains directories: %v", compact)
	}
	if _, exists := compact["scripts"]; exists {
		t.Fatalf("compact manifest unexpectedly contains scripts: %v", compact)
	}
}

func readPkgManifestFiles(t *testing.T, pkgPath string) ([]byte, []byte) {
	t.Helper()

	pkgData, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatalf("read pkg: %v", err)
	}
	decoder, err := zstd.NewReader(bytes.NewReader(pkgData))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer decoder.Close()

	tarReader := tar.NewReader(decoder)
	var manifestBody []byte
	var compactManifestBody []byte

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar entry: %v", err)
		}

		switch header.Name {
		case "+MANIFEST":
			body, err := io.ReadAll(tarReader)
			if err != nil {
				t.Fatalf("read %s body: %v", header.Name, err)
			}
			manifestBody = body
		case "+COMPACT_MANIFEST":
			body, err := io.ReadAll(tarReader)
			if err != nil {
				t.Fatalf("read %s body: %v", header.Name, err)
			}
			compactManifestBody = body
		default:
			if _, err := io.Copy(io.Discard, tarReader); err != nil {
				t.Fatalf("discard %s body: %v", header.Name, err)
			}
		}
		if len(manifestBody) > 0 && len(compactManifestBody) > 0 {
			break
		}
	}

	if len(manifestBody) == 0 {
		t.Fatal("missing +MANIFEST in pkg archive")
	}
	if len(compactManifestBody) == 0 {
		t.Fatal("missing +COMPACT_MANIFEST in pkg archive")
	}
	return manifestBody, compactManifestBody
}

func Test_patchPackageSite(t *testing.T) {
	t.Parallel()

	pluginVer := "2024.1.0_1"
	binaryVer := "2024.1.0"
	pluginURL := "https://example.com/os-cloudflared.pkg"
	binaryURL := "https://example.com/cloudflared.pkg"

	t.Run("file not found returns error", func(t *testing.T) {
		t.Parallel()
		_, err := patchPackageSite(
			filepath.Join(t.TempDir(), "nope.ndjson"),
			pluginVer, binaryVer, pluginURL, binaryURL,
		)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected ErrNotExist, got %v", err)
		}
	})

	t.Run("non-JSON lines are passed through unchanged", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "in.ndjson")
		raw := "not json at all\n"
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL)
		if err != nil {
			t.Fatal(err)
		}
		if got != raw {
			t.Fatalf("got %q want %q", got, raw)
		}
	})

	t.Run("patches os-cloudflared line with matching version", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "in.ndjson")
		line := ndjsonLine(map[string]any{
			"name":     "os-cloudflared",
			"version":  pluginVer,
			"path":     "old-plugin",
			"repopath": "old-plugin",
		})
		if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL)
		if err != nil {
			t.Fatal(err)
		}
		objs := parseNDJSON(t, got)
		if len(objs) != 1 {
			t.Fatalf("want 1 object, got %d", len(objs))
		}
		o := objs[0]
		if o["path"] != pluginURL || o["repopath"] != pluginURL {
			t.Fatalf("path/repopath: %+v", o)
		}
	})

	t.Run("patches cloudflared binary line with matching version", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "in.ndjson")
		line := ndjsonLine(map[string]any{
			"name":     "cloudflared",
			"version":  binaryVer,
			"path":     "old-bin",
			"repopath": "old-bin",
		})
		if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL)
		if err != nil {
			t.Fatal(err)
		}
		objs := parseNDJSON(t, got)
		if len(objs) != 1 {
			t.Fatalf("want 1 object, got %d", len(objs))
		}
		o := objs[0]
		if o["path"] != binaryURL || o["repopath"] != binaryURL {
			t.Fatalf("path/repopath: %+v", o)
		}
	})

	t.Run("non-matching name/version leaves path and repopath unchanged", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "in.ndjson")
		line := ndjsonLine(map[string]any{
			"name":     "cloudflared",
			"version":  "other",
			"path":     "keep-me",
			"repopath": "keep-me-too",
		})
		if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL)
		if err != nil {
			t.Fatal(err)
		}
		objs := parseNDJSON(t, got)
		if len(objs) != 1 {
			t.Fatalf("want 1 object, got %d", len(objs))
		}
		o := objs[0]
		if o["path"] != "keep-me" || o["repopath"] != "keep-me-too" {
			t.Fatalf("path/repopath: %+v", o)
		}
	})

	t.Run("trailing newline on input is handled", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "in.ndjson")
		content := ndjsonLine(map[string]any{"name": "x", "version": "1"}) + "\n\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL)
		if err != nil {
			t.Fatal(err)
		}
		objs := parseNDJSON(t, got)
		if len(objs) != 1 {
			t.Fatalf("want 1 object, got %d", len(objs))
		}
	})

	t.Run("empty lines are skipped", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "in.ndjson")
		l1 := ndjsonLine(map[string]any{"name": "a", "version": "1"})
		l2 := ndjsonLine(map[string]any{"name": "b", "version": "2"})
		raw := l1 + "\n\n" + l2 + "\n"
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("want 2 output lines, got %d: %q", len(lines), got)
		}
	})
}

func Test_copyFile(t *testing.T) {
	t.Parallel()

	t.Run("copies content correctly with given mode", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(tmp, "dst.txt")
		if err := copyFile(src, dst, 0644); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "hello" {
			t.Fatalf("content: %q", data)
		}
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0777 != 0644 {
			t.Fatalf("mode: %v", fi.Mode())
		}
	})

	t.Run("creates parent directories if missing", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "a.txt")
		if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(tmp, "nested", "deep", "b.txt")
		if err := copyFile(src, dst, 0644); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "x" {
			t.Fatalf("content: %q", data)
		}
	})

	t.Run("source not found returns error", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		err := copyFile(filepath.Join(tmp, "missing"), filepath.Join(tmp, "out"), 0644)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("mkdirall fails when parent path is a file", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		block := filepath.Join(tmp, "block")
		if err := os.WriteFile(block, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(tmp, "src.txt")
		if err := os.WriteFile(src, []byte("y"), 0644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(block, "nested", "out.txt")
		err := copyFile(src, dst, 0644)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("open destination fails when destination is a directory", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		if err := os.WriteFile(src, []byte("z"), 0644); err != nil {
			t.Fatal(err)
		}
		dstDir := filepath.Join(tmp, "dstdir")
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			t.Fatal(err)
		}
		err := copyFile(src, dstDir, 0644)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func Test_copyTree(t *testing.T) {
	t.Parallel()

	t.Run("copies directory tree preserving structure and contents", func(t *testing.T) {
		t.Parallel()
		srcRoot := t.TempDir()
		if err := os.MkdirAll(filepath.Join(srcRoot, "sub"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcRoot, "root.txt"), []byte("r"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcRoot, "sub", "nested.txt"), []byte("n"), 0644); err != nil {
			t.Fatal(err)
		}
		dstRoot := filepath.Join(t.TempDir(), "dst")
		if err := copyTree(srcRoot, dstRoot); err != nil {
			t.Fatal(err)
		}
		rdata, err := os.ReadFile(filepath.Join(dstRoot, "root.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(rdata) != "r" {
			t.Fatalf("root: %q", rdata)
		}
		ndata, err := os.ReadFile(filepath.Join(dstRoot, "sub", "nested.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(ndata) != "n" {
			t.Fatalf("nested: %q", ndata)
		}
	})

	t.Run("source not found returns error", func(t *testing.T) {
		t.Parallel()
		err := copyTree(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "dst"))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("destination is an existing file", func(t *testing.T) {
		t.Parallel()
		srcRoot := t.TempDir()
		dstFile := filepath.Join(t.TempDir(), "dst")
		if err := os.WriteFile(dstFile, []byte("block"), 0644); err != nil {
			t.Fatal(err)
		}
		err := copyTree(srcRoot, dstFile)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func Test_createZstdTar_extractZstdTar(t *testing.T) {
	t.Parallel()

	t.Run("round-trip content matches original", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		orig := filepath.Join(tmp, "original.txt")
		want := []byte("payload data\n")
		if err := os.WriteFile(orig, want, 0644); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(tmp, "out.tar.zst")
		if err := createZstdTar(archive, orig, "inner/name.txt"); err != nil {
			t.Fatal(err)
		}
		extracted := filepath.Join(tmp, "extracted.txt")
		if err := extractZstdTar(archive, "name.txt", extracted); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(extracted)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("extract matches full header name", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		orig := filepath.Join(tmp, "original.txt")
		if err := os.WriteFile(orig, []byte("full"), 0644); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(tmp, "full.tar.zst")
		nameInArchive := "repo/pkg/file.txt"
		if err := createZstdTar(archive, orig, nameInArchive); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(tmp, "out.txt")
		if err := extractZstdTar(archive, nameInArchive, out); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "full" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("invalid zstd stream returns error", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		bad := filepath.Join(tmp, "bad.zst")
		if err := os.WriteFile(bad, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
			t.Fatal(err)
		}
		err := extractZstdTar(bad, "x", filepath.Join(tmp, "out.txt"))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid tar after zstd returns error from tar reader", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		var buf bytes.Buffer
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write([]byte("this is not a tar archive")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		arc := filepath.Join(tmp, "bad.tar.zst")
		if err := os.WriteFile(arc, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
		err = extractZstdTar(arc, "any.txt", filepath.Join(tmp, "out.txt"))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("extract output path is an existing directory", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		orig := filepath.Join(tmp, "f.txt")
		if err := os.WriteFile(orig, []byte("q"), 0644); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(tmp, "a.tar.zst")
		if err := createZstdTar(archive, orig, "only.txt"); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(tmp, "outdir")
		if err := os.MkdirAll(outDir, 0755); err != nil {
			t.Fatal(err)
		}
		err := extractZstdTar(archive, "only.txt", outDir)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("extract missing member returns not found error", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		orig := filepath.Join(tmp, "f.txt")
		if err := os.WriteFile(orig, []byte("z"), 0644); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(tmp, "a.tar.zst")
		if err := createZstdTar(archive, orig, "only.txt"); err != nil {
			t.Fatal(err)
		}
		err := extractZstdTar(archive, "ghost.txt", filepath.Join(tmp, "out.txt"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("error: %v", err)
		}
	})

	t.Run("extract with non-existent archive returns error", func(t *testing.T) {
		t.Parallel()
		err := extractZstdTar(
			filepath.Join(t.TempDir(), "missing.tar.zst"),
			"x",
			filepath.Join(t.TempDir(), "out.txt"),
		)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("create with non-existent source file returns error", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		err := createZstdTar(
			filepath.Join(tmp, "out.tar.zst"),
			filepath.Join(tmp, "missing.txt"),
			"name",
		)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("create fails when archive path is an existing directory", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		orig := filepath.Join(tmp, "src.txt")
		if err := os.WriteFile(orig, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		archiveDir := filepath.Join(tmp, "archive.zst")
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			t.Fatal(err)
		}
		err := createZstdTar(archiveDir, orig, "name")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// withStateFiles temporarily overrides the stateFile and revisionFile package
// vars to point at files inside dir, then restores them after the test.
func withStateFiles(t *testing.T, dir string) {
	t.Helper()
	origState := stateFile
	origRevision := revisionFile
	stateFile = filepath.Join(dir, "state")
	revisionFile = filepath.Join(dir, "revision")
	t.Cleanup(func() {
		stateFile = origState
		revisionFile = origRevision
	})
}

func Test_readState(t *testing.T) {
	t.Run("returns empty string when file missing", func(t *testing.T) {
		withStateFiles(t, t.TempDir())
		got := readState()
		if got != "" {
			t.Fatalf("readState() = %q, want empty", got)
		}
	})

	t.Run("returns trimmed content when file present", func(t *testing.T) {
		dir := t.TempDir()
		withStateFiles(t, dir)
		if err := os.WriteFile(stateFile, []byte("2026.3.0\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got := readState()
		if got != "2026.3.0" {
			t.Fatalf("readState() = %q, want %q", got, "2026.3.0")
		}
	})
}

func Test_readRevision(t *testing.T) {
	t.Run("returns 1 and error when file missing", func(t *testing.T) {
		withStateFiles(t, t.TempDir())
		got, err := readRevision()
		if got != 1 {
			t.Fatalf("readRevision() = %d, want 1", got)
		}
		if err == nil {
			t.Fatal("readRevision: expected error for missing file")
		}
	})

	t.Run("returns parsed integer when file present", func(t *testing.T) {
		dir := t.TempDir()
		withStateFiles(t, dir)
		if err := os.WriteFile(revisionFile, []byte("5\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := readRevision()
		if err != nil {
			t.Fatalf("readRevision: %v", err)
		}
		if got != 5 {
			t.Fatalf("readRevision() = %d, want 5", got)
		}
	})

	t.Run("returns error when file contains non-integer", func(t *testing.T) {
		dir := t.TempDir()
		withStateFiles(t, dir)
		if err := os.WriteFile(revisionFile, []byte("notanumber\n"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readRevision()
		if err == nil {
			t.Fatal("readRevision: expected error for invalid integer")
		}
		var numErr *strconv.NumError
		if !errors.As(err, &numErr) {
			t.Fatalf("readRevision: expected *strconv.NumError, got %T: %v", err, err)
		}
	})
}

func Test_saveState(t *testing.T) {
	t.Run("writes version and revision to separate files", func(t *testing.T) {
		dir := t.TempDir()
		withStateFiles(t, dir)
		saveState("2026.3.0", 3)
		stateData, err := os.ReadFile(stateFile)
		if err != nil {
			t.Fatalf("read stateFile: %v", err)
		}
		if strings.TrimSpace(string(stateData)) != "2026.3.0" {
			t.Fatalf("stateFile = %q, want %q", stateData, "2026.3.0")
		}
		revData, err := os.ReadFile(revisionFile)
		if err != nil {
			t.Fatalf("read revisionFile: %v", err)
		}
		if strings.TrimSpace(string(revData)) != "3" {
			t.Fatalf("revisionFile = %q, want %q", revData, "3")
		}
	})

	t.Run("round-trip: saveState then readState/readRevision return same values", func(t *testing.T) {
		dir := t.TempDir()
		withStateFiles(t, dir)
		saveState("2025.1.0", 7)
		gotVersion := readState()
		if gotVersion != "2025.1.0" {
			t.Fatalf("readState() = %q, want %q", gotVersion, "2025.1.0")
		}
		gotRevision, err := readRevision()
		if err != nil {
			t.Fatalf("readRevision: %v", err)
		}
		if gotRevision != 7 {
			t.Fatalf("readRevision() = %d, want 7", gotRevision)
		}
	})
}
