package main

import (
	"os"
	"strings"
	"testing"
)

func TestREADMEIncludesCopyPasteCommands(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("ReadFile README.md: %v", err)
	}
	readme := string(body)

	want := []string{
		"go install github.com/AnkanMisra/fast@latest",
		"fast --simultaneous",
		"$env:FAST_NO_UPDATE_NOTIFIER=\"1\"; fast",
		"set FAST_NO_UPDATE_NOTIFIER=1 && fast",
		"set FAST_NO_UPDATE_NOTIFIER=1 && fast --version",
		"git tag v0.1.0",
		"git push origin v0.1.0",
	}
	for _, snippet := range want {
		if !strings.Contains(readme, snippet) {
			t.Fatalf("README.md is missing copy-paste command %q", snippet)
		}
	}
}
