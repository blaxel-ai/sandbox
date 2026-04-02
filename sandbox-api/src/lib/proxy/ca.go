package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	extraCACertsEnv       = "SANDBOX_EXTRA_CA_CERTS"
	defaultSystemCABundle = "/etc/ssl/certs/ca-certificates.crt"
	defaultMergedCAPath   = "/etc/ssl/certs/sandbox-ca-bundle.crt"
)

// runtimeCAEnvVars lists the environment variables that various runtimes and
// tools use to locate CA certificates. Each entry indicates whether the var
// should point to the merged bundle (system + extra) or only the extra CAs.
//
//   - SSL_CERT_FILE      – Go's crypto/x509, OpenSSL-based CLIs
//   - REQUESTS_CA_BUNDLE – Python requests / httpx
//   - CURL_CA_BUNDLE     – curl
//   - NODE_EXTRA_CA_CERTS – Node.js (appends to its compiled-in Mozilla CAs,
//     so it only needs the extra certificates)
var runtimeCAEnvVars = []struct {
	Name      string
	UseMerged bool
}{
	{"SSL_CERT_FILE", true},
	{"REQUESTS_CA_BUNDLE", true},
	{"CURL_CA_BUNDLE", true},
	{"NODE_EXTRA_CA_CERTS", false},
}

// MergeCABundle reads extra CA certificates from the file specified by
// SANDBOX_EXTRA_CA_CERTS and appends them to the system trust store. The
// combined bundle is written to a well-known path and the appropriate
// environment variables are set so that Go, Node.js, Python, and curl all
// trust the additional CAs.
//
// If SANDBOX_EXTRA_CA_CERTS is unset or empty the function is a no-op.
func MergeCABundle() error {
	extraPath := os.Getenv(extraCACertsEnv)
	if extraPath == "" {
		return nil
	}

	extraCerts, err := os.ReadFile(extraPath)
	if err != nil {
		return fmt.Errorf("read extra CA certs from %s: %w", extraPath, err)
	}
	if !looksLikePEM(extraCerts) {
		return fmt.Errorf("%s does not appear to contain PEM-encoded certificates", extraPath)
	}

	logrus.Infof("proxy/ca: merging extra CA certificates from %s", extraPath)

	systemPath := systemCABundlePath()
	systemCerts, _ := os.ReadFile(systemPath)

	merged := appendPEMBundles(systemCerts, extraCerts)

	outPath := mergedCAOutputPath()
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("create directory for merged CA bundle: %w", err)
	}
	if err := os.WriteFile(outPath, merged, 0644); err != nil {
		return fmt.Errorf("write merged CA bundle: %w", err)
	}

	logrus.Infof("proxy/ca: wrote merged CA bundle to %s (%d bytes)", outPath, len(merged))

	for _, v := range runtimeCAEnvVars {
		target := outPath
		if !v.UseMerged {
			target = extraPath
		}
		if err := os.Setenv(v.Name, target); err != nil {
			logrus.WithError(err).Warnf("proxy/ca: failed to set %s", v.Name)
			continue
		}
		logrus.Infof("proxy/ca: %s=%s", v.Name, target)
	}

	return nil
}

func systemCABundlePath() string {
	if p := os.Getenv("SSL_CERT_FILE"); p != "" {
		return p
	}
	return defaultSystemCABundle
}

func mergedCAOutputPath() string {
	if p := os.Getenv("SANDBOX_MERGED_CA_PATH"); p != "" {
		return p
	}
	return defaultMergedCAPath
}

func looksLikePEM(data []byte) bool {
	return strings.Contains(string(data), "-----BEGIN ")
}

// appendPEMBundles concatenates two PEM bundles, ensuring a newline separator.
func appendPEMBundles(base, extra []byte) []byte {
	if len(base) == 0 {
		return extra
	}
	if base[len(base)-1] != '\n' {
		base = append(base, '\n')
	}
	return append(base, extra...)
}
