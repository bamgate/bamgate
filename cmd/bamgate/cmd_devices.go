package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	ltable "github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/control"
)

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List and manage devices on your network",
	Long: `List all devices registered to your bamgate network, showing their
connection status, capabilities, and configuration.

Devices that are currently online show their ICE connection type and
advertised capabilities (routes, DNS, search domains). Use
'bamgate devices configure' to choose what to accept from each device.`,
	RunE: runDevicesList,
}

var devicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered devices",
	RunE:  runDevicesList,
}

var devicesRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a device so it can no longer connect",
	RunE:  runDevicesRevoke,
}

var devicesConfigureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Interactively configure what to accept from devices",
	Long: `Open an interactive TUI to select which routes, DNS servers, and
search domains to accept from each online device. Selections are saved
to the config file and applied immediately.`,
	RunE: runDevicesConfigure,
}

func init() {
	devicesCmd.AddCommand(devicesListCmd)
	devicesCmd.AddCommand(devicesRevokeCmd)
	devicesCmd.AddCommand(devicesConfigureCmd)
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

// getJWT borrows the current JWT access token from the running bamgate daemon
// via its control socket. This avoids doing a token refresh (which rotates the
// single-use refresh token and requires root to persist the new one).
//
// If the daemon is not running, it falls back to loading the config and
// refreshing the token directly (with a warning about persistence).
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

	// Try to borrow the JWT from the running daemon first.
	socketPath := control.ResolveSocketPath()
	token, err := control.FetchToken(socketPath)
	if err == nil && token != "" {
		return token, baseURL, cfg, nil
	}

	// Daemon not running — fall back to direct refresh.
	fmt.Fprintf(os.Stderr, "Warning: bamgate daemon is not running. Refreshing token directly.\n")
	fmt.Fprintf(os.Stderr, "  This rotates the refresh token. Run 'bamgate up' to avoid this.\n")

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

// peerInfoByName holds live peer data from the running daemon, keyed by peer name.
type peerInfoByName struct {
	status   *control.PeerStatus
	offering *control.PeerOfferings
}

// liveState holds daemon state: whether it's reachable and per-peer data.
type liveState struct {
	daemonRunning bool
	peers         map[string]peerInfoByName
}

// fetchLivePeers queries the running daemon for connected peer status and
// offerings. Returns daemonRunning=false (not an error) if the daemon is offline.
func fetchLivePeers() liveState {
	socketPath := control.ResolveSocketPath()

	peers := make(map[string]peerInfoByName)

	// Fetch status (peer connection info).
	status, err := control.FetchStatus(socketPath)
	if err != nil {
		// Daemon not running — that's fine, just no live data.
		return liveState{daemonRunning: false, peers: peers}
	}
	for i := range status.Peers {
		p := &status.Peers[i]
		info := peers[p.ID]
		info.status = p
		peers[p.ID] = info
	}

	// Fetch offerings (capabilities).
	offerings, err := control.FetchOfferings(socketPath)
	if err == nil {
		for i := range offerings {
			o := &offerings[i]
			info := peers[o.PeerID]
			info.offering = o
			peers[o.PeerID] = info
		}
	}

	return liveState{daemonRunning: true, peers: peers}
}

func runDevicesList(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()

	jwt, baseURL, cfg, err := getJWT(cfgPath)
	if err != nil {
		return err
	}

	// Fetch registered devices from the server.
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

	// Fetch live peer data from the daemon (best-effort).
	live := fetchLivePeers()

	// Build table rows.
	var rows [][]string
	hasConfigurableDevices := false

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorYellow)).
		Bold(true)
	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorBg4))

	for _, d := range result.Devices {
		name := d.DeviceName
		isThisDevice := d.DeviceID == cfg.Network.DeviceID
		if isThisDevice {
			name += " (this device)"
		}

		// Determine status: revoked > this device (online) > peer online > offline.
		status := "offline"
		iceType := "-"
		var caps []string

		if d.Revoked {
			status = styleRevoked.Render("revoked")
		} else if live.daemonRunning && isThisDevice {
			status = styleActive.Render("online")
		} else if live.daemonRunning {
			if info, ok := live.peers[d.DeviceName]; ok && info.status != nil {
				status = styleActive.Render(info.status.State)
				iceType = info.status.ICEType
				if iceType == "" {
					iceType = "-"
				}

				// Summarize advertised capabilities.
				if info.offering != nil {
					if len(info.offering.Advertised.Routes) > 0 {
						accepted := len(info.offering.Accepted.Routes)
						total := len(info.offering.Advertised.Routes)
						caps = append(caps, fmt.Sprintf("routes: %d/%d", accepted, total))
						hasConfigurableDevices = true
					}
					if len(info.offering.Advertised.DNS) > 0 {
						accepted := len(info.offering.Accepted.DNS)
						total := len(info.offering.Advertised.DNS)
						caps = append(caps, fmt.Sprintf("dns: %d/%d", accepted, total))
						hasConfigurableDevices = true
					}
					if len(info.offering.Advertised.DNSSearch) > 0 {
						accepted := len(info.offering.Accepted.DNSSearch)
						total := len(info.offering.Advertised.DNSSearch)
						caps = append(caps, fmt.Sprintf("search: %d/%d", accepted, total))
						hasConfigurableDevices = true
					}
				}
			}
		}

		capsStr := "-"
		if len(caps) > 0 {
			capsStr = strings.Join(caps, ", ")
		}

		created := time.Unix(d.CreatedAt, 0).Format("2006-01-02")

		rows = append(rows, []string{name, d.Address, status, iceType, capsStr, created})
	}

	cellStyle := lipgloss.NewStyle().PaddingRight(2)

	t := ltable.New().
		Headers("NAME", "ADDRESS", "STATUS", "ICE TYPE", "CAPABILITIES", "CREATED").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == ltable.HeaderRow {
				return headerStyle.PaddingRight(2)
			}
			return cellStyle
		})

	fmt.Println(t)

	if hasConfigurableDevices {
		fmt.Println("Use 'bamgate devices configure' to choose what to accept.")
	}

	return nil
}

func runDevicesRevoke(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()

	jwt, baseURL, cfg, err := getJWT(cfgPath)
	if err != nil {
		return err
	}

	// Fetch devices to build the selection list.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := auth.ListDevices(ctx, baseURL, jwt)
	if err != nil {
		return err
	}

	// Filter to revocable devices (not this device, not already revoked).
	var options []huh.Option[string]
	for _, d := range result.Devices {
		if d.DeviceID == cfg.Network.DeviceID {
			continue // can't revoke yourself
		}
		if d.Revoked {
			continue // already revoked
		}
		label := fmt.Sprintf("%s (%s)", d.DeviceName, d.Address)
		options = append(options, huh.NewOption(label, d.DeviceID))
	}

	if len(options) == 0 {
		fmt.Println("No devices available to revoke.")
		return nil
	}

	var targetID string
	selectForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a device to revoke").
				Description("The device will no longer be able to connect.").
				Options(options...).
				Value(&targetID),
		),
	).WithTheme(customHuhTheme())

	if err := selectForm.Run(); err != nil {
		return fmt.Errorf("cancelled")
	}

	// Confirm.
	var confirmed bool
	// Find the device name for the confirmation message.
	var targetName string
	for _, d := range result.Devices {
		if d.DeviceID == targetID {
			targetName = d.DeviceName
			break
		}
	}

	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Revoke %q?", targetName)).
				Description("This device will be disconnected and unable to reconnect.").
				Affirmative("Revoke").
				Negative("Cancel").
				Value(&confirmed),
		),
	).WithTheme(customHuhTheme())

	if err := confirmForm.Run(); err != nil {
		return fmt.Errorf("cancelled")
	}

	if !confirmed {
		fmt.Println("Cancelled.")
		return nil
	}

	revokeCtx, revokeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer revokeCancel()

	if err := auth.RevokeDevice(revokeCtx, baseURL, jwt, targetID); err != nil {
		return err
	}

	fmt.Printf("Device %q has been revoked.\n", targetName)
	return nil
}

func runDevicesConfigure(cmd *cobra.Command, args []string) error {
	socketPath := control.ResolveSocketPath()

	offerings, err := control.FetchOfferings(socketPath)
	if err != nil {
		return fmt.Errorf("is bamgate running? %w", err)
	}

	if len(offerings) == 0 {
		fmt.Println("No devices connected.")
		return nil
	}

	// Filter to devices that have something to offer.
	var configurableDevices []control.PeerOfferings
	for _, o := range offerings {
		if len(o.Advertised.Routes) > 0 || len(o.Advertised.DNS) > 0 || len(o.Advertised.DNSSearch) > 0 {
			configurableDevices = append(configurableDevices, o)
		}
	}

	if len(configurableDevices) == 0 {
		fmt.Println("No devices have capabilities to configure.")
		return nil
	}

	// Build a form for each device.
	for _, o := range configurableDevices {
		fmt.Fprintf(os.Stderr, "\nConfiguring device: %s (%s)\n", o.PeerID, o.Address)

		var selectedRoutes, selectedDNS, selectedSearch []string
		var formFields []huh.Field

		// Routes multi-select
		if len(o.Advertised.Routes) > 0 {
			routeOptions := make([]huh.Option[string], len(o.Advertised.Routes))
			for i, r := range o.Advertised.Routes {
				routeOptions[i] = huh.NewOption(r, r)
			}
			selectedRoutes = append([]string{}, o.Accepted.Routes...)
			formFields = append(formFields,
				huh.NewMultiSelect[string]().
					Title("Routes").
					Description("Select subnet routes to accept from this device").
					Options(routeOptions...).
					Value(&selectedRoutes),
			)
		}

		// DNS multi-select
		if len(o.Advertised.DNS) > 0 {
			dnsOptions := make([]huh.Option[string], len(o.Advertised.DNS))
			for i, d := range o.Advertised.DNS {
				dnsOptions[i] = huh.NewOption(d, d)
			}
			selectedDNS = append([]string{}, o.Accepted.DNS...)
			formFields = append(formFields,
				huh.NewMultiSelect[string]().
					Title("DNS Servers").
					Description("Select DNS servers to use from this device").
					Options(dnsOptions...).
					Value(&selectedDNS),
			)
		}

		// Search domains multi-select
		if len(o.Advertised.DNSSearch) > 0 {
			searchOptions := make([]huh.Option[string], len(o.Advertised.DNSSearch))
			for i, s := range o.Advertised.DNSSearch {
				searchOptions[i] = huh.NewOption(s, s)
			}
			selectedSearch = append([]string{}, o.Accepted.DNSSearch...)
			formFields = append(formFields,
				huh.NewMultiSelect[string]().
					Title("Search Domains").
					Description("Select DNS search domains to use from this device").
					Options(searchOptions...).
					Value(&selectedSearch),
			)
		}

		if len(formFields) == 0 {
			continue
		}

		form := huh.NewForm(
			huh.NewGroup(formFields...),
		).WithTheme(customHuhTheme())

		if err := form.Run(); err != nil {
			return fmt.Errorf("form cancelled: %w", err)
		}

		// Send the selections to the running agent.
		req := control.ConfigureRequest{
			PeerID: o.PeerID,
			Selections: control.PeerCapabilities{
				Routes:    selectedRoutes,
				DNS:       selectedDNS,
				DNSSearch: selectedSearch,
			},
		}

		if err := control.SendConfigure(socketPath, req); err != nil {
			return fmt.Errorf("applying configuration for %s: %w", o.PeerID, err)
		}

		fmt.Fprintf(os.Stderr, "Saved selections for %s.\n", o.PeerID)
		printSelectionSummary(selectedRoutes, selectedDNS, selectedSearch)
	}

	return nil
}

func printSelectionSummary(routes, dns, search []string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if len(routes) > 0 {
		fmt.Fprintf(w, "  Routes:\t%s\n", strings.Join(routes, ", "))
	}
	if len(dns) > 0 {
		fmt.Fprintf(w, "  DNS:\t%s\n", strings.Join(dns, ", "))
	}
	if len(search) > 0 {
		fmt.Fprintf(w, "  Search:\t%s\n", strings.Join(search, ", "))
	}
	w.Flush()
}
