package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/config"
)

var genkeyCmd = &cobra.Command{
	Use:   "genkey",
	Short: "Generate a new WireGuard private key",
	Long: `Generate a new Curve25519 private key suitable for WireGuard.
The private key is printed to stdout as base64. The corresponding
public key is printed to stderr.

Example:
  bamgate genkey                    # print private key
  bamgate genkey 2>/dev/null        # private key only (pipe-friendly)`,
	RunE: runGenkey,
}

func runGenkey(cmd *cobra.Command, args []string) error {
	privKey, err := config.GeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	pubKey := config.PublicKey(privKey)

	// Private key to stdout (pipe-friendly).
	fmt.Println(privKey.String())

	// Public key to stderr (informational).
	fmt.Fprintf(cmd.ErrOrStderr(), "public key: %s\n", pubKey.String())

	return nil
}
