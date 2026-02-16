package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/control"
)

var peersCmd = &cobra.Command{
	Use:   "peers",
	Short: "Show peer capabilities and selections",
	Long: `Display connected peers with their advertised capabilities (routes,
DNS servers, search domains) and what you have currently accepted from each.

Use 'bamgate peers configure' to interactively choose what to accept.`,
	RunE: runPeers,
}

var peersConfigureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Interactively configure what to accept from peers",
	Long: `Open an interactive TUI to select which routes, DNS servers, and
search domains to accept from each connected peer. Selections are saved
to the config file and applied immediately.`,
	RunE: runPeersConfigure,
}

func init() {
	peersCmd.AddCommand(peersConfigureCmd)
}

func runPeers(cmd *cobra.Command, args []string) error {
	offerings, err := control.FetchOfferings(control.ResolveSocketPath())
	if err != nil {
		return fmt.Errorf("is bamgate running? %w", err)
	}

	if len(offerings) == 0 {
		fmt.Println("No peers connected.")
		return nil
	}

	for i, o := range offerings {
		if i > 0 {
			fmt.Println()
		}
		fmt.Fprintf(os.Stdout, "%s      %s\n", styleKey.Render("Peer:"), o.PeerID)
		fmt.Fprintf(os.Stdout, "%s   %s\n", styleKey.Render("Address:"), o.Address)
		fmt.Fprintf(os.Stdout, "%s     %s\n", styleKey.Render("State:"), o.State)

		// Routes
		if len(o.Advertised.Routes) > 0 {
			fmt.Fprintf(os.Stdout, "\n  %s\n", styleHeader.Render("Routes (advertised):"))
			for _, r := range o.Advertised.Routes {
				accepted := contains(o.Accepted.Routes, r)
				marker := " "
				if accepted {
					marker = styleActive.Render("*")
				}
				fmt.Fprintf(os.Stdout, "    [%s] %s\n", marker, r)
			}
		}

		// DNS
		if len(o.Advertised.DNS) > 0 {
			fmt.Fprintf(os.Stdout, "\n  %s\n", styleHeader.Render("DNS Servers (advertised):"))
			for _, d := range o.Advertised.DNS {
				accepted := contains(o.Accepted.DNS, d)
				marker := " "
				if accepted {
					marker = styleActive.Render("*")
				}
				fmt.Fprintf(os.Stdout, "    [%s] %s\n", marker, d)
			}
		}

		// Search domains
		if len(o.Advertised.DNSSearch) > 0 {
			fmt.Fprintf(os.Stdout, "\n  %s\n", styleHeader.Render("Search Domains (advertised):"))
			for _, s := range o.Advertised.DNSSearch {
				accepted := contains(o.Accepted.DNSSearch, s)
				marker := " "
				if accepted {
					marker = styleActive.Render("*")
				}
				fmt.Fprintf(os.Stdout, "    [%s] %s\n", marker, s)
			}
		}

		// Show if peer has no capabilities
		if len(o.Advertised.Routes) == 0 && len(o.Advertised.DNS) == 0 && len(o.Advertised.DNSSearch) == 0 {
			fmt.Fprintf(os.Stdout, "  No capabilities advertised.\n")
		}
	}

	fmt.Println()
	fmt.Println("[*] = accepted. Use 'bamgate peers configure' to change.")

	return nil
}

func runPeersConfigure(cmd *cobra.Command, args []string) error {
	socketPath := control.ResolveSocketPath()

	offerings, err := control.FetchOfferings(socketPath)
	if err != nil {
		return fmt.Errorf("is bamgate running? %w", err)
	}

	if len(offerings) == 0 {
		fmt.Println("No peers connected.")
		return nil
	}

	// Filter to peers that have something to offer.
	var configurablePeers []control.PeerOfferings
	for _, o := range offerings {
		if len(o.Advertised.Routes) > 0 || len(o.Advertised.DNS) > 0 || len(o.Advertised.DNSSearch) > 0 {
			configurablePeers = append(configurablePeers, o)
		}
	}

	if len(configurablePeers) == 0 {
		fmt.Println("No peers have capabilities to configure.")
		return nil
	}

	// Build a form for each peer.
	for _, o := range configurablePeers {
		fmt.Fprintf(os.Stderr, "\nConfiguring peer: %s (%s)\n", o.PeerID, o.Address)

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
					Description("Select subnet routes to accept from this peer").
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
					Description("Select DNS servers to use from this peer").
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
					Description("Select DNS search domains to use from this peer").
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
		printSelectionSummary(o.PeerID, selectedRoutes, selectedDNS, selectedSearch)
	}

	return nil
}

func printSelectionSummary(peerID string, routes, dns, search []string) {
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

// contains checks if a string slice contains a specific value.
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
