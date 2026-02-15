package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
)

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "Manage registered devices",
	Long:  `List or revoke devices registered to your bamgate network.`,
}

var devicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered devices",
	RunE:  runDevicesList,
}

var devicesRevokeCmd = &cobra.Command{
	Use:   "revoke <device-id>",
	Short: "Revoke a device so it can no longer connect",
	Args:  cobra.ExactArgs(1),
	RunE:  runDevicesRevoke,
}

func init() {
	devicesCmd.AddCommand(devicesListCmd)
	devicesCmd.AddCommand(devicesRevokeCmd)
}

// httpBaseURL converts the WSS signaling URL from config to an HTTPS base URL
// suitable for REST API calls.
func httpBaseURL(serverURL string) string {
	u := serverURL
	u = strings.Replace(u, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	if idx := strings.Index(u, "/connect"); idx != -1 {
		u = u[:idx]
	}
	return u
}

// getJWT loads the config, refreshes the JWT, and returns the access token
// along with the HTTP base URL and config. The rotated refresh token is
// persisted back to the config file.
func getJWT(cfgPath string) (jwt, baseURL string, cfg *config.Config, err error) {
	cfg, err = config.LoadConfig(cfgPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("loading config: %w", err)
	}

	if cfg.Network.ServerURL == "" {
		return "", "", nil, fmt.Errorf("server_url not configured — run 'bamgate setup' first")
	}
	if cfg.Network.DeviceID == "" || cfg.Network.RefreshToken == "" {
		return "", "", nil, fmt.Errorf("device not registered — run 'bamgate setup' first")
	}

	baseURL = httpBaseURL(cfg.Network.ServerURL)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := auth.Refresh(ctx, baseURL, cfg.Network.DeviceID, cfg.Network.RefreshToken)
	if err != nil {
		return "", "", nil, fmt.Errorf("authenticating: %w", err)
	}

	// Persist the rotated refresh token immediately.
	cfg.Network.RefreshToken = resp.RefreshToken
	if saveErr := config.SaveSecrets(cfgPath, cfg); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not persist rotated refresh token: %v\n", saveErr)
	}

	return resp.AccessToken, baseURL, cfg, nil
}

func runDevicesList(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()

	jwt, baseURL, cfg, err := getJWT(cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := auth.ListDevices(ctx, baseURL, jwt)
	if err != nil {
		return err
	}

	if len(result.Devices) == 0 {
		fmt.Println("No devices registered.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "DEVICE ID\tNAME\tADDRESS\tSTATUS\tCREATED\tLAST SEEN")

	for _, d := range result.Devices {
		status := "active"
		if d.Revoked {
			status = "revoked"
		}

		// Mark this device.
		name := d.DeviceName
		if d.DeviceID == cfg.Network.DeviceID {
			name += " (this device)"
		}

		created := time.Unix(d.CreatedAt, 0).Format("2006-01-02 15:04")
		lastSeen := "-"
		if d.LastSeenAt != nil {
			lastSeen = time.Unix(*d.LastSeenAt, 0).Format("2006-01-02 15:04")
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			d.DeviceID, name, d.Address, status, created, lastSeen)
	}

	return w.Flush()
}

func runDevicesRevoke(cmd *cobra.Command, args []string) error {
	targetID := args[0]
	cfgPath := resolvedConfigPath()

	jwt, baseURL, cfg, err := getJWT(cfgPath)
	if err != nil {
		return err
	}

	if targetID == cfg.Network.DeviceID {
		return fmt.Errorf("cannot revoke your own device")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := auth.RevokeDevice(ctx, baseURL, jwt, targetID); err != nil {
		return err
	}

	fmt.Printf("Device %s has been revoked.\n", targetID)
	return nil
}
