package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	ucl "github.com/nahanni/go-ucl"
)

const cloudflaredPackageOrigin = "net/cloudflared"

func init() {
	ucl.Ucldebug = false
	ucl.UclExportKeyOrder = false
}

func parseUCLManifest(manifestUCL string) (map[string]json.RawMessage, error) {
	parser := ucl.NewParser(strings.NewReader(manifestUCL))
	parsed, err := parser.Ucl()
	if err != nil {
		slog.Error("parse ucl manifest failed", "err", err)
		return nil, fmt.Errorf("parse ucl: %w", err)
	}
	delete(parsed, ucl.KeyOrder)

	data, err := json.Marshal(parsed)
	if err != nil {
		slog.Error("marshal parsed manifest failed", "err", err)
		return nil, fmt.Errorf("marshal parsed manifest: %w", err)
	}

	manifest := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		slog.Error("unmarshal manifest json failed", "err", err)
		return nil, fmt.Errorf("unmarshal manifest json: %w", err)
	}
	return manifest, nil
}
