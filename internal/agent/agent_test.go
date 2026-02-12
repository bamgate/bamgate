package agent

import "testing"

func TestIsValidRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		route string
		want  bool
	}{
		{"192.168.1.0/24", true},
		{"10.0.0.0/8", true},
		{"172.16.0.0/12", true},
		{"fd00::/8", true},
		{"10.0.0.1/32", true},

		// Dangerous routes that should be rejected.
		{"0.0.0.0/0", false},
		{"::/0", false},

		// Invalid CIDR.
		{"not-a-cidr", false},
		{"192.168.1.0", false}, // missing prefix length
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			t.Parallel()
			if got := isValidRoute(tt.route); got != tt.want {
				t.Errorf("isValidRoute(%q) = %v, want %v", tt.route, got, tt.want)
			}
		})
	}
}
