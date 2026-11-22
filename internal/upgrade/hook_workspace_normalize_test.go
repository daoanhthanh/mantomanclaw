package upgrade

import (
	"os"
	"testing"
)

func TestIsStaleWorkspace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"docker era app workspace", "/app/workspace/clax", true},
		{"docker era root", "/app/workspace", true},
		{"docker era trailing slash", "/app/workspace/", true},
		{"tilde literal", "~/.mantomanagent/x-workspace", true},
		{"tilde only", "~", true},
		{"absolute host path", "/var/lib/mantomanagent/workspace/clax", false},
		{"current dot", ".", false},
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"custom absolute (preserve)", "/srv/agents/clax", false},
		{"relative non-tilde", "data/workspace", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStaleWorkspace(tc.in); got != tc.want {
				t.Fatalf("isStaleWorkspace(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveWorkspaceBase_EnvWins(t *testing.T) {
	t.Setenv("MANTOMANAGENT_WORKSPACE", "/var/lib/mantomanagent/workspace")
	if got := resolveWorkspaceBase(); got != "/var/lib/mantomanagent/workspace" {
		t.Fatalf("resolveWorkspaceBase() = %q, want /var/lib/mantomanagent/workspace", got)
	}
}

func TestResolveWorkspaceBase_EnvTilde(t *testing.T) {
	t.Setenv("MANTOMANAGENT_WORKSPACE", "~/custom/ws")
	home, _ := os.UserHomeDir()
	want := home + "/custom/ws"
	if got := resolveWorkspaceBase(); got != want {
		t.Fatalf("resolveWorkspaceBase() = %q, want %q", got, want)
	}
}

func TestResolveWorkspaceBase_StripsTrailingSlash(t *testing.T) {
	t.Setenv("MANTOMANAGENT_WORKSPACE", "/var/lib/mantomanagent/workspace/")
	if got := resolveWorkspaceBase(); got != "/var/lib/mantomanagent/workspace" {
		t.Fatalf("resolveWorkspaceBase() = %q, want trimmed", got)
	}
}

func TestResolveWorkspaceBase_FallbackOnMissingConfig(t *testing.T) {
	t.Setenv("MANTOMANAGENT_WORKSPACE", "")
	t.Setenv("MANTOMANAGENT_CONFIG", "/nonexistent/path/config.json")
	// Should not return empty even if config file missing — falls back through
	// config.Load's IsNotExist branch (which returns defaults) or to default.
	if got := resolveWorkspaceBase(); got == "" {
		t.Fatal("resolveWorkspaceBase() returned empty for missing config; expected fallback")
	}
}
