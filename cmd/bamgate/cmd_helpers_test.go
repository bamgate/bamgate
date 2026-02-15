package main

import (
	"testing"
)

func TestNormalizeServerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "no scheme prepends wss",
			input: "bamgate.example.workers.dev/connect",
			want:  "wss://bamgate.example.workers.dev/connect",
		},
		{
			name:  "wss scheme unchanged",
			input: "wss://bamgate.example.workers.dev/connect",
			want:  "wss://bamgate.example.workers.dev/connect",
		},
		{
			name:  "ws scheme unchanged",
			input: "ws://localhost:8080/connect",
			want:  "ws://localhost:8080/connect",
		},
		{
			name:  "https converted to wss",
			input: "https://bamgate.example.workers.dev/connect",
			want:  "wss://bamgate.example.workers.dev/connect",
		},
		{
			name:  "http converted to ws",
			input: "http://localhost:8080/connect",
			want:  "ws://localhost:8080/connect",
		},
		{
			name:  "leading and trailing whitespace trimmed",
			input: "  bamgate.example.workers.dev/connect  ",
			want:  "wss://bamgate.example.workers.dev/connect",
		},
		{
			name:    "empty string errors",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace-only errors",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "unsupported scheme errors",
			input:   "ftp://example.com/connect",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeServerURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeServerURL(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeServerURL(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("normalizeServerURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
