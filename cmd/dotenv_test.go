package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	content := "# comment\nexport MANTOMANAGENT_FROM_FILE=from-file\nMANTOMANAGENT_ALREADY=ignored\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MANTOMANAGENT_ALREADY", "preset")
	t.Setenv("MANTOMANAGENT_FROM_FILE", "")

	loadDotEnv()

	if got := os.Getenv("MANTOMANAGENT_FROM_FILE"); got != "from-file" {
		t.Fatalf("MANTOMANAGENT_FROM_FILE = %q", got)
	}
	if got := os.Getenv("MANTOMANAGENT_ALREADY"); got != "preset" {
		t.Fatalf("existing env overwritten: %q", got)
	}
}
