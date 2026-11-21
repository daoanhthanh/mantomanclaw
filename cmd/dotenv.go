package cmd

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
)

// loadDotEnv loads KEY=VALUE lines from .env in the working directory.
// Existing environment variables win. A missing file is not an error.
// Compose also reads this file; its service environment block overrides it.
func loadDotEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		_ = os.Setenv(key, val)
	}
	if err := sc.Err(); err != nil {
		slog.Warn("read .env", "error", err)
	}
}
