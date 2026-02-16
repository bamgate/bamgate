package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show config file paths",
	Long: `Print the paths to the bamgate configuration files.

  bamgate config          Print config file paths
  bamgate config edit     Open config.toml in $EDITOR (requires sudo for secrets)
  bamgate config path     Print only the config directory path`,
	RunE: runConfig,
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config files in $EDITOR",
	Long: `Open the bamgate config.toml and secrets.toml in your editor.
Requires sudo to edit secrets.toml.`,
	RunE: runConfigEdit,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config directory path",
	RunE:  runConfigPath,
}

func init() {
	configCmd.AddCommand(configEditCmd)
	configCmd.AddCommand(configPathCmd)
}

func runConfig(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	secretsPath := config.SecretsPathFromConfig(cfgPath)

	fmt.Fprintf(os.Stdout, "Config:  %s\n", cfgPath)
	fmt.Fprintf(os.Stdout, "Secrets: %s\n", secretsPath)

	// Show file status.
	for _, p := range []string{cfgPath, secretsPath} {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(os.Stdout, "  %s  %s\n", info.Mode().Perm(), p)
	}

	return nil
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Try common editors.
		for _, e := range []string{"nano", "vim", "vi"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found — set $EDITOR")
	}

	cfgPath := resolvedConfigPath()
	secretsPath := config.SecretsPathFromConfig(cfgPath)

	// Open both files in the editor.
	c := exec.Command(editor, cfgPath, secretsPath)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		return fmt.Errorf("editor exited: %w", err)
	}

	return nil
}

func runConfigPath(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	fmt.Println(filepath.Dir(cfgPath))
	return nil
}
