package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishReleaseOnlyRejectsInvalidFlags(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "cloudflared-builder")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build publisher: %v\n%s", err, output)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "run cannot ignore release-only",
			args: []string{"-release-only", "-version", "2026.9.2", "run"},
			want: "-release-only requires the plan or publish command",
		},
		{
			name: "missing version",
			args: []string{"-release-only", "publish"},
			want: "-release-only requires -version",
		},
		{
			name: "preview cannot publish a release",
			args: []string{"-release-only", "-version", "2026.9.2", "-preview", "publish"},
			want: "-release-only and -preview are mutually exclusive",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			outputFile := filepath.Join(t.TempDir(), "outputs")
			command := exec.Command(binary, testCase.args...)
			command.Env = append(os.Environ(), "GITHUB_OUTPUT="+outputFile)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("publisher accepted invalid flags: %s", output)
			}
			if !strings.Contains(string(output), testCase.want) {
				t.Fatalf("publisher error = %s, want %s", output, testCase.want)
			}
			if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
				t.Fatalf("publisher wrote workflow outputs before rejecting flags: %v", err)
			}
		})
	}
}
