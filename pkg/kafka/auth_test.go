package kafka

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAuthOptsNil(t *testing.T) {
	opts, err := buildAuthOpts(nil)
	require.NoError(t, err)
	assert.Nil(t, opts)
}

func TestBuildAuthOptsPlain(t *testing.T) {
	opts, err := buildAuthOpts(&scenario.KafkaAuth{
		Type:     scenario.KafkaAuthSASLPlain,
		Username: "u", Password: "p",
	})
	require.NoError(t, err)
	assert.Len(t, opts, 2, "dialer + SASL")
}

func TestBuildAuthOptsScramVariants(t *testing.T) {
	for _, typ := range []scenario.KafkaAuthType{scenario.KafkaAuthSASLScram256, scenario.KafkaAuthSASLScram512} {
		opts, err := buildAuthOpts(&scenario.KafkaAuth{Type: typ, Username: "u", Password: "p"})
		require.NoError(t, err, "type %s", typ)
		assert.Len(t, opts, 2, "dialer + SASL for %s", typ)
	}
}

func TestBuildAuthOptsMTLSMissingFiles(t *testing.T) {
	_, err := buildAuthOpts(&scenario.KafkaAuth{Type: scenario.KafkaAuthMTLS})
	require.Error(t, err, "missing cert/key must error")
}

func TestBuildAuthOptsMTLSBadPath(t *testing.T) {
	_, err := buildAuthOpts(&scenario.KafkaAuth{
		Type:     scenario.KafkaAuthMTLS,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	})
	require.Error(t, err)
}

func TestBuildAuthOptsOAuthNotYet(t *testing.T) {
	_, err := buildAuthOpts(&scenario.KafkaAuth{Type: scenario.KafkaAuthOAuthBearer})
	require.ErrorContains(t, err, "M3")
}

func TestBuildAuthOptsAWSIAMNotYet(t *testing.T) {
	_, err := buildAuthOpts(&scenario.KafkaAuth{Type: scenario.KafkaAuthAWSIAM})
	require.ErrorContains(t, err, "M3")
}

func TestBuildAuthOptsUnknown(t *testing.T) {
	_, err := buildAuthOpts(&scenario.KafkaAuth{Type: "garbage"})
	require.ErrorContains(t, err, "unknown auth type")
}

// writePEMPair writes a minimal self-signed TLS cert+key pair for tests
// that need the file paths to resolve, even if the crypto is thrown away.
func writePEMPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	cert := `-----BEGIN CERTIFICATE-----
MIIBIjCByqADAgECAgEBMAoGCCqGSM49BAMCMA8xDTALBgNVBAMTBHJvb3QwHhcN
MjYwMTAxMDAwMDAwWhcNMzYwMTAxMDAwMDAwWjAPMQ0wCwYDVQQDEwRsZWFmMFkw
EwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEmbMw5J8xx5M4q6qYyI8xjK1c+9gQZ3C5
2X4cJv0pYx5rJ9G1b4Zq9x8pYl7rJ9G1b4Zq9x8pYl7rJ9G1b4ZqMAoGCCqGSM49
BAMCA0gAMEUCIQC1234567890abcdefgACIAbcdefghij1234567890
-----END CERTIFICATE-----
`
	key := `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIN2Tv6vKvB1yYZ5jvYxB9XkV4yI9zTlYzG+k3XKCQHWQoAoGCCqGSM49
AwEHoUQDQgAEmbMw5J8xx5M4q6qYyI8xjK1c+9gQZ3C52X4cJv0pYx5rJ9G1b4Zq
9x8pYl7rJ9G1b4Zq9x8pYl7rJ9G1b4Zq
-----END EC PRIVATE KEY-----
`
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, []byte(cert), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte(key), 0o600))
	return
}

func TestBuildAuthOptsMTLSInvalidPEMContent(t *testing.T) {
	certPath, keyPath := writePEMPair(t)
	// These stubs are invalid crypto → tls.LoadX509KeyPair must reject.
	_, err := buildAuthOpts(&scenario.KafkaAuth{
		Type:     scenario.KafkaAuthMTLS,
		CertFile: certPath,
		KeyFile:  keyPath,
	})
	require.Error(t, err)
}
