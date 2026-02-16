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

  bamgate config                Print config file paths
  bamgate config edit           Open config.toml in $EDITOR
  bamgate config edit --secrets Open secrets.toml in $EDITOR
  bamgate config path           Print the config directory path`,
	RunE: runConfig,
}

var editSecrets bool

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config.toml in $EDITOR",
	Long: `Open the bamgate config.toml in your editor. Use --secrets to
open secrets.toml instead (contains private key, tokens, etc.).`,
	RunE: runConfigEdit,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config directory path",
	RunE:  runConfigPath,
}

func init() {
	configEditCmd.Flags().BoolVar(&editSecrets, "secrets", false, "edit secrets.toml instead of config.toml")
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

	target := resolvedConfigPath()
	if editSecrets {
		target = config.SecretsPathFromConfig(target)
	}

	c := exec.Command(editor, target)
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
