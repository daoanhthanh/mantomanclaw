package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

func installCmd() *cobra.Command {
	var installDir string
	var force bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the mantomanagent binary to a system PATH directory",
		Long: `Copies the running binary to a directory on PATH so that
'mantomanagent' is available system-wide as a CLI/runtime package command.

Default install directories:
  Linux/macOS  /usr/local/bin  (falls back to ~/.local/bin if not writable)
  Windows      %USERPROFILE%\AppData\Local\Programs\mantomanagent

The skill at skills/mantomanagent/SKILL.md expects the binary to be reachable
via 'command -v mantomanagent' or at the standard runtime paths:
  /app/data/.runtime/bin/mantomanagent
  /var/lib/mantomanagent/data/.runtime/bin/mantomanagent

Run 'mantomanagent install --dir <path>' to choose a custom directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(installDir, force)
		},
	}

	cmd.Flags().StringVar(&installDir, "dir", "", "target directory (must be on PATH)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing installation")
	return cmd
}

func runInstall(dir string, force bool) error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving current binary: %w", err)
	}
	// Follow symlinks so we copy the real binary, not a wrapper.
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("resolving symlinks for %s: %w", src, err)
	}

	target, err := resolveInstallDir(dir)
	if err != nil {
		return err
	}

	binaryName := "mantomanagent"
	if runtime.GOOS == "windows" {
		binaryName = "mantomanagent.exe"
	}
	dst := filepath.Join(target, binaryName)

	if !force {
		if _, statErr := os.Stat(dst); statErr == nil {
			return fmt.Errorf("%s already exists — use --force to overwrite", dst)
		}
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", target, err)
	}

	if err := copyBinary(src, dst); err != nil {
		return err
	}

	fmt.Printf("✓ Installed %s → %s\n", binaryName, dst)
	fmt.Println()
	fmt.Println("Verify:")
	fmt.Println("  command -v mantomanagent")
	fmt.Println("  mantomanagent version")
	fmt.Println("  mantomanagent --help")
	fmt.Println()
	fmt.Printf("If %s is not on your PATH, add it:\n", target)
	if runtime.GOOS == "windows" {
		fmt.Printf("  [Environment]::SetEnvironmentVariable('PATH', $env:PATH + ';%s', 'User')\n", target)
	} else {
		fmt.Printf("  export PATH=\"%s:$PATH\"\n", target)
	}
	return nil
}

// resolveInstallDir returns the first writable candidate when dir is empty.
func resolveInstallDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}

	if runtime.GOOS == "windows" {
		base := os.Getenv("USERPROFILE")
		if base == "" {
			base = os.Getenv("APPDATA")
		}
		return filepath.Join(base, "AppData", "Local", "Programs", "mantomanagent"), nil
	}

	candidates := []string{
		"/usr/local/bin",
		filepath.Join(homeDir(), ".local", "bin"),
	}
	for _, c := range candidates {
		if isWritable(c) {
			return c, nil
		}
	}
	// Fall back to ~/.local/bin even if it doesn't exist yet; MkdirAll handles it.
	return filepath.Join(homeDir(), ".local", "bin"), nil
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

func isWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		// Doesn't exist — treat as writable (MkdirAll will create it).
		return errors.Is(err, os.ErrNotExist)
	}
	if !info.IsDir() {
		return false
	}
	// Probe by attempting a temp file.
	tmp, err := os.CreateTemp(dir, ".mantomanagent-write-check-*")
	if err != nil {
		return false
	}
	tmp.Close()
	os.Remove(tmp.Name())
	return true
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source binary: %w", err)
	}
	defer in.Close()

	// Write to a temp file alongside dst, then rename — atomic on most FS.
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("creating %s: %w\n  (try running with sudo or --dir ~/.local/bin)", tmp, err)
	}

	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copying binary: %w", err)
	}
	if err = out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("flushing %s: %w", tmp, err)
	}

	if err = os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("installing to %s: %w\n  (try running with sudo or --dir ~/.local/bin)", dst, err)
	}
	return nil
}
