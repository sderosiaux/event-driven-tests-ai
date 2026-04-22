// Package kafka wraps franz-go to produce and consume records per scenario
// steps, exposing a minimal surface the orchestrator can drive.
package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// buildAuthOpts turns a scenario.KafkaAuth into kgo options.
// Returns nil,nil when auth is nil (plaintext, no TLS).
func buildAuthOpts(auth *scenario.KafkaAuth) ([]kgo.Opt, error) {
	if auth == nil {
		return nil, nil
	}
	switch auth.Type {
	case scenario.KafkaAuthSASLPlain:
		return []kgo.Opt{
			saslTLSDialer(),
			kgo.SASL(plain.Auth{User: auth.Username, Pass: auth.Password}.AsMechanism()),
		}, nil

	case scenario.KafkaAuthSASLScram256:
		return []kgo.Opt{
			saslTLSDialer(),
			kgo.SASL(scram.Auth{User: auth.Username, Pass: auth.Password}.AsSha256Mechanism()),
		}, nil

	case scenario.KafkaAuthSASLScram512:
		return []kgo.Opt{
			saslTLSDialer(),
			kgo.SASL(scram.Auth{User: auth.Username, Pass: auth.Password}.AsSha512Mechanism()),
		}, nil

	case scenario.KafkaAuthMTLS:
		cfg, err := buildMTLSConfig(auth)
		if err != nil {
			return nil, err
		}
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: cfg}
		return []kgo.Opt{kgo.Dialer(d.DialContext)}, nil

	case scenario.KafkaAuthOAuthBearer:
		return nil, fmt.Errorf("kafka: SASL/OAUTHBEARER is wired in M3, not available in M1")
	case scenario.KafkaAuthAWSIAM:
		return nil, fmt.Errorf("kafka: AWS IAM is wired in M3, not available in M1")

	default:
		return nil, fmt.Errorf("kafka: unknown auth type %q", auth.Type)
	}
}

// saslTLSDialer returns a 10s-timeout TLS dialer — the canonical franz-go recipe
// for SASL over a TLS-protected connection (most managed Kafka offerings).
// Callers that need plaintext SASL can override by passing no-op kgo.Dialer later.
func saslTLSDialer() kgo.Opt {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}}
	return kgo.Dialer(d.DialContext)
}

func buildMTLSConfig(auth *scenario.KafkaAuth) (*tls.Config, error) {
	if auth.CertFile == "" || auth.KeyFile == "" {
		return nil, fmt.Errorf("kafka: mtls requires cert_file and key_file")
	}
	cert, err := tls.LoadX509KeyPair(auth.CertFile, auth.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("kafka: load mtls keypair: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if auth.CAFile != "" {
		ca, err := os.ReadFile(auth.CAFile)
		if err != nil {
			return nil, fmt.Errorf("kafka: read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("kafka: ca_file is not a PEM bundle")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
