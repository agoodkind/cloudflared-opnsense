package main

import (
	"encoding/json"
	"log/slog"
	"strings"
)

type packageDependency struct {
	Version string `json:"version"`
	Origin  string `json:"origin"`
}

type dependencyField string

const (
	dependencyFieldVersion dependencyField = "version"
	dependencyFieldOrigin  dependencyField = "origin"
)

func pluginManifestWithCloudflaredDependency(
	manifestUCL string,
	cfVersion string,
) string {
	manifest := strings.TrimRight(manifestUCL, "\n")
	if strings.Contains(manifest, "\ndeps") || strings.HasPrefix(manifest, "deps") {
		return manifest + "\n"
	}

	depsBlock := strings.Join([]string{
		"deps: {",
		"    cloudflared: {",
		"        version: \"" + cfVersion + "\"",
		"        origin: \"net/cloudflared\"",
		"    }",
		"}",
		"",
	}, "\n")
	return manifest + "\n" + depsBlock
}

func parseDepsBlock(
	lines []string,
	startLineIndex int,
) (map[string]packageDependency, int, bool) {
	line := strings.TrimSpace(lines[startLineIndex])
	_, bodyStart, found := strings.Cut(line, ":")
	if !found {
		return nil, startLineIndex, false
	}
	if !strings.HasPrefix(strings.TrimSpace(bodyStart), "{") {
		return nil, startLineIndex, false
	}

	deps := make(map[string]packageDependency)
	for lineIndex := startLineIndex + 1; lineIndex < len(lines); lineIndex++ {
		line = strings.TrimSpace(lines[lineIndex])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "}" {
			return deps, lineIndex, true
		}
		if !strings.HasSuffix(line, "{") {
			continue
		}

		name := strings.TrimSpace(strings.TrimSuffix(line, "{"))
		name = strings.TrimSpace(strings.TrimSuffix(name, ":"))
		name = strings.Trim(name, "\"")
		dependency, nextLineIndex, ok := parseDependencyBlock(lines, lineIndex)
		if ok && name != "" {
			deps[name] = dependency
			lineIndex = nextLineIndex
		}
	}
	return nil, startLineIndex, false
}

func parseDependencyBlock(
	lines []string,
	startLineIndex int,
) (packageDependency, int, bool) {
	dependency := packageDependency{}
	for lineIndex := startLineIndex + 1; lineIndex < len(lines); lineIndex++ {
		line := strings.TrimSpace(lines[lineIndex])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "}" || line == "}," {
			return dependency, lineIndex, true
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), "\"")
		value = strings.TrimSpace(value)
		value = strings.TrimSuffix(value, ",")
		value = strings.Trim(strings.TrimSpace(value), "\"")
		switch dependencyField(key) {
		case dependencyFieldVersion:
			dependency.Version = value
		case dependencyFieldOrigin:
			dependency.Origin = value
		}
	}
	return dependency, startLineIndex, false
}

func jsonRawFromPackageDependencyMap(
	m map[string]packageDependency,
) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		slog.Error("encode dependency map failed", "err", err)
		return json.RawMessage("null")
	}
	return b
}
