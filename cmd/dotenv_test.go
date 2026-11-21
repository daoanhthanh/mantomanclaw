package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	content := "# comment\nexport GOCLAW_FROM_FILE=from-file\nGOCLAW_ALREADY=ignored\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCLAW_ALREADY", "preset")
	t.Setenv("GOCLAW_FROM_FILE", "")

	loadDotEnv()

	if got := os.Getenv("GOCLAW_FROM_FILE"); got != "from-file" {
		t.Fatalf("GOCLAW_FROM_FILE = %q", got)
	}
	if got := os.Getenv("GOCLAW_ALREADY"); got != "preset" {
		t.Fatalf("existing env overwritten: %q", got)
	}
}
