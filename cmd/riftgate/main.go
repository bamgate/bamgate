// Command riftgate is a WireGuard VPN tunnel over WebRTC. It allows a single
// user to access their home network from anywhere without exposing the home
// network's public IP.
package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// Global flags shared across subcommands.
var (
	globalConfigPath string
	globalVerbose    bool
	globalLogger     *slog.Logger
)

// rootCmd is the top-level command.
var rootCmd = &cobra.Command{
	Use:   "riftgate",
	Short: "WireGuard VPN tunnel over WebRTC",
	Long: `riftgate lets you access your home network from anywhere without
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
	rootCmd.PersistentFlags().StringVar(&globalConfigPath, "config", "", "path to config file (default: ~/.config/riftgate/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&globalVerbose, "verbose", "v", false, "enable debug logging")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(genkeyCmd)
	rootCmd.AddCommand(installCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
