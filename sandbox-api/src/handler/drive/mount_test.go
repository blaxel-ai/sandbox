package drive

import "testing"

func TestParseFilerAddress(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "IPv4 nameserver",
			content: "nameserver 172.16.1.126\n",
			want:    "172.16.1.126",
		},
		{
			name:    "IPv6 nameserver",
			content: "# DNS Configuration\nnameserver 2600:1f14:c75:3900::301\n",
			want:    "2600:1f14:c75:3900::301",
		},
		{
			name:    "IPv6 nameserver with zone",
			content: "nameserver fe80::1%eth0\n",
			want:    "fe80::1%eth0",
		},
		{
			name:    "skips malformed nameserver",
			content: "nameserver not-an-ip\nnameserver 10.0.0.2\n",
			want:    "10.0.0.2",
		},
		{
			name:    "requires exact nameserver directive",
			content: "nameserver-proxy 10.0.0.2\n",
			wantErr: true,
		},
		{
			name:    "missing nameserver",
			content: "options edns0 trust-ad\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFilerAddress([]byte(tt.content))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseFilerAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseFilerAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatFilerServerAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "IPv4",
			address: "172.16.1.126",
			want:    "172.16.1.126:49200.49201",
		},
		{
			name:    "IPv6",
			address: "2600:1f14:c75:3900::301",
			want:    "2600:1f14:c75:3900::301:49200.49201",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatFilerServerAddress(tt.address); got != tt.want {
				t.Fatalf("formatFilerServerAddress(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}
