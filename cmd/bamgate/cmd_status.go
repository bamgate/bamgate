package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/control"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show connection status",
	Long:  `Query the running bamgate agent and display connected peers, connection type (direct/relayed), and tunnel addresses.`,
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	status, err := control.FetchStatus(control.ResolveSocketPath())
	if err != nil {
		return fmt.Errorf("is bamgate running? %w", err)
	}

	// Print header.
	fmt.Fprintf(os.Stdout, "Device:    %s\n", status.Device)
	fmt.Fprintf(os.Stdout, "Address:   %s\n", status.Address)
	fmt.Fprintf(os.Stdout, "Server:    %s\n", status.ServerURL)
	fmt.Fprintf(os.Stdout, "Uptime:    %s\n", formatDuration(time.Duration(status.UptimeSeconds*float64(time.Second))))
	fmt.Fprintf(os.Stdout, "Peers:     %d\n", len(status.Peers))
	fmt.Println()

	if len(status.Peers) == 0 {
		fmt.Println("No peers connected.")
		return nil
	}

	// Print peer table.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PEER\tADDRESS\tSTATE\tICE TYPE\tROUTES\tCONNECTED")
	for _, p := range status.Peers {
		routes := "-"
		if len(p.Routes) > 0 {
			routes = fmt.Sprintf("%v", p.Routes)
		}
		connected := "-"
		if !p.ConnectedSince.IsZero() {
			connected = formatDuration(time.Since(p.ConnectedSince)) + " ago"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			p.ID, p.Address, p.State, p.ICEType, routes, connected)
	}
	w.Flush()

	return nil
}

// formatDuration formats a duration into a human-readable string like "2h15m" or "45s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
