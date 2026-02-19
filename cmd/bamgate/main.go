// Command bamgate is a WireGuard VPN tunnel over WebRTC. It allows a single
// user to access their home network from anywhere without exposing the home
// network's public IP.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=...".
// GoReleaser sets this automatically from the git tag.
var version = "dev"

// Global flags shared across subcommands.
var (
	globalConfigPath string
	globalVerbose    bool
	globalLogger     *slog.Logger
)

// rootCmd is the top-level command.
var rootCmd = &cobra.Command{
	Use:   "bamgate",
	Short: "WireGuard VPN tunnel over WebRTC",
	Long: `bamgate lets you access your home network from anywhere without
exposing the home network's public IP. It tunnels WireGuard traffic
over WebRTC data channels, using Cloudflare Workers for signaling.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		level := slog.LevelInfo
		if globalVerbose {
			level = slog.LevelDebug
		}
		globalLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		}))
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&globalConfigPath, "config", "", "path to config file (default: /etc/bamgate/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&globalVerbose, "verbose", "v", false, "enable debug logging")

	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(devicesCmd)
	rootCmd.AddCommand(workerCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(qrCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(genkeyCmd)
	rootCmd.AddCommand(versionCmd)
}

// versionCmd prints the build version.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the bamgate version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version)
	},
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
