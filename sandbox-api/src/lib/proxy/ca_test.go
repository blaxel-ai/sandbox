package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakePEM = `-----BEGIN CERTIFICATE-----
MIIBkTCB+wIUEbGRdm6E1Z4P8sCY5kCVUMEfEoUwDQYJKoZIhvcNAQELBQAwEj
EQMA4GA1UEAwwHZmFrZS1jYTAeFw0yNTAxMDEwMDAwMDBaFw0zNTAxMDEwMDAw
MDBaMBIxEDAOBgNVBAMMB2Zha2UtY2EwXDANBgkqhkiG9w0BAQEFAANLADBIAkEA
-----END CERTIFICATE-----
`

func TestMergeCABundle_NoOp(t *testing.T) {
	t.Setenv(extraCACertsEnv, "")

	if err := MergeCABundle(); err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
}

func TestMergeCABundle_FileNotFound(t *testing.T) {
	t.Setenv(extraCACertsEnv, "/no/such/file.pem")

	if err := MergeCABundle(); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMergeCABundle_NotPEM(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.txt")
	os.WriteFile(bad, []byte("this is not a certificate"), 0644)

	t.Setenv(extraCACertsEnv, bad)

	err := MergeCABundle()
	if err == nil {
		t.Fatal("expected error for non-PEM file")
	}
	if !strings.Contains(err.Error(), "PEM") {
		t.Errorf("error should mention PEM: %v", err)
	}
}

func TestMergeCABundle_MergesWithSystemBundle(t *testing.T) {
	dir := t.TempDir()

	systemBundle := filepath.Join(dir, "system-ca.crt")
	systemPEM := "-----BEGIN CERTIFICATE-----\nSYSTEM\n-----END CERTIFICATE-----\n"
	os.WriteFile(systemBundle, []byte(systemPEM), 0644)

	extraFile := filepath.Join(dir, "extra-ca.crt")
	os.WriteFile(extraFile, []byte(fakePEM), 0644)

	mergedPath := filepath.Join(dir, "merged.crt")

	t.Setenv(extraCACertsEnv, extraFile)
	t.Setenv("SSL_CERT_FILE", systemBundle)
	t.Setenv("SANDBOX_MERGED_CA_PATH", mergedPath)

	if err := MergeCABundle(); err != nil {
		t.Fatalf("MergeCABundle: %v", err)
	}

	merged, err := os.ReadFile(mergedPath)
	if err != nil {
		t.Fatalf("read merged bundle: %v", err)
	}

	if !strings.Contains(string(merged), "SYSTEM") {
		t.Error("merged bundle should contain system certificates")
	}
	if !strings.Contains(string(merged), "MIIBkTCB") {
		t.Error("merged bundle should contain extra certificates")
	}

	if got := os.Getenv("SSL_CERT_FILE"); got != mergedPath {
		t.Errorf("SSL_CERT_FILE = %q, want %q", got, mergedPath)
	}
	if got := os.Getenv("REQUESTS_CA_BUNDLE"); got != mergedPath {
		t.Errorf("REQUESTS_CA_BUNDLE = %q, want %q", got, mergedPath)
	}
	if got := os.Getenv("CURL_CA_BUNDLE"); got != mergedPath {
		t.Errorf("CURL_CA_BUNDLE = %q, want %q", got, mergedPath)
	}
	if got := os.Getenv("NODE_EXTRA_CA_CERTS"); got != extraFile {
		t.Errorf("NODE_EXTRA_CA_CERTS = %q, want %q (should be extra-only path)", got, extraFile)
	}
}

func TestMergeCABundle_NoSystemBundle(t *testing.T) {
	dir := t.TempDir()

	extraFile := filepath.Join(dir, "extra-ca.crt")
	os.WriteFile(extraFile, []byte(fakePEM), 0644)

	mergedPath := filepath.Join(dir, "merged.crt")

	t.Setenv(extraCACertsEnv, extraFile)
	t.Setenv("SSL_CERT_FILE", filepath.Join(dir, "nonexistent-system.crt"))
	t.Setenv("SANDBOX_MERGED_CA_PATH", mergedPath)

	if err := MergeCABundle(); err != nil {
		t.Fatalf("MergeCABundle: %v", err)
	}

	merged, err := os.ReadFile(mergedPath)
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}

	if string(merged) != fakePEM {
		t.Errorf("when no system bundle exists, merged should equal extra CAs.\ngot:\n%s", merged)
	}
}

func TestMergeCABundle_NewlineSeparator(t *testing.T) {
	dir := t.TempDir()

	systemPEM := "-----BEGIN CERTIFICATE-----\nSYSTEM\n-----END CERTIFICATE-----"
	systemBundle := filepath.Join(dir, "sys.crt")
	os.WriteFile(systemBundle, []byte(systemPEM), 0644)

	extraFile := filepath.Join(dir, "extra.crt")
	os.WriteFile(extraFile, []byte(fakePEM), 0644)

	mergedPath := filepath.Join(dir, "merged.crt")

	t.Setenv(extraCACertsEnv, extraFile)
	t.Setenv("SSL_CERT_FILE", systemBundle)
	t.Setenv("SANDBOX_MERGED_CA_PATH", mergedPath)

	if err := MergeCABundle(); err != nil {
		t.Fatal(err)
	}

	merged, _ := os.ReadFile(mergedPath)
	if strings.Contains(string(merged), "----------") {
		t.Error("certificates should be separated by a newline, not concatenated on the same line")
	}
}

func TestAppendPEMBundles(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		extra string
		want  string
	}{
		{
			name:  "empty base",
			base:  "",
			extra: "EXTRA",
			want:  "EXTRA",
		},
		{
			name:  "base with trailing newline",
			base:  "BASE\n",
			extra: "EXTRA",
			want:  "BASE\nEXTRA",
		},
		{
			name:  "base without trailing newline",
			base:  "BASE",
			extra: "EXTRA",
			want:  "BASE\nEXTRA",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(appendPEMBundles([]byte(tc.base), []byte(tc.extra)))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLooksLikePEM(t *testing.T) {
	if looksLikePEM([]byte("not a cert")) {
		t.Error("plain text should not look like PEM")
	}
	if !looksLikePEM([]byte("-----BEGIN CERTIFICATE-----\ndata\n-----END CERTIFICATE-----")) {
		t.Error("valid PEM should be recognised")
	}
	if !looksLikePEM([]byte("-----BEGIN RSA PRIVATE KEY-----\ndata")) {
		t.Error("any PEM block type should be recognised")
	}
}
