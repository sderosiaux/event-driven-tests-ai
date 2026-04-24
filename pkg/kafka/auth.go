// Package kafka wraps franz-go to produce and consume records per scenario
// steps, exposing a minimal surface the orchestrator can drive.
package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/aws"
	"github.com/twmb/franz-go/pkg/sasl/oauth"
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
		fetcher, err := newOIDCTokenFetcher(auth)
		if err != nil {
			return nil, err
		}
		return []kgo.Opt{
			saslTLSDialer(),
			kgo.SASL(oauth.Oauth(fetcher)),
		}, nil

	case scenario.KafkaAuthAWSIAM:
		mech, err := awsIAMMechanism(auth)
		if err != nil {
			return nil, err
		}
		return []kgo.Opt{saslTLSDialer(), kgo.SASL(mech)}, nil

	default:
		return nil, fmt.Errorf("kafka: unknown auth type %q", auth.Type)
	}
}

// awsIAMMechanism builds a SASL mechanism for MSK-style IAM authentication.
//
// When the scenario supplies explicit username + password, these are treated
// as static AccessKey/SecretKey and used verbatim — useful for CI pipelines
// that prefer scoped keys staged out of band.
//
// When neither is set, we fall back to the AWS SDK default credential chain
// (env vars → shared ~/.aws → ECS task role → IRSA → EC2 instance profile).
// The chain's own caching + refresh handles session expiry, so franz-go can
// invoke the fetcher on every new SASL session without amplifying IMDS load.
func awsIAMMechanism(auth *scenario.KafkaAuth) (sasl.Mechanism, error) {
	if auth.Username != "" && auth.Password != "" {
		// Scenario-supplied credentials are kept isolated from the ambient
		// environment. An AWS_SESSION_TOKEN left over on the host must not
		// be silently paired with a different access/secret pair — that mix
		// produces stale-token auth failures after the ambient session
		// expires.
		creds := aws.Auth{
			AccessKey: auth.Username,
			SecretKey: auth.Password,
			UserAgent: "edt/" + auth.Region,
		}
		return creds.AsManagedStreamingIAMMechanism(), nil
	}

	// Build the default chain once. The returned config carries a cached
	// CredentialsProvider whose Retrieve() method handles refresh internally.
	cfgOpts := []func(*awsconfig.LoadOptions) error{}
	if auth.Region != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithRegion(auth.Region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: aws_iam: load default config: %w", err)
	}
	if cfg.Credentials == nil {
		return nil, fmt.Errorf("kafka: aws_iam: no credentials provider in default chain")
	}

	fetcher := func(ctx context.Context) (aws.Auth, error) {
		c, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return aws.Auth{}, fmt.Errorf("kafka: aws_iam: retrieve credentials: %w", err)
		}
		return aws.Auth{
			AccessKey:    c.AccessKeyID,
			SecretKey:    c.SecretAccessKey,
			SessionToken: c.SessionToken,
			UserAgent:    "edt/" + auth.Region,
		}, nil
	}
	return aws.ManagedStreamingIAM(fetcher), nil
}
// for SASL over a TLS-protected connection (most managed Kafka offerings).
// Callers that need plaintext SASL can override by passing no-op kgo.Dialer later.
func saslTLSDialer() kgo.Opt {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}}
	return kgo.Dialer(d.DialContext)
}

// newOIDCTokenFetcher returns a thread-safe closure that fetches and caches
// a bearer token via the OAuth 2.0 client_credentials grant. franz-go calls
// the closure each time it needs a fresh SASL session; we re-use a cached
// token until it is within 60s of expiry, then refresh.
//
// Required fields on auth: TokenURL, ClientID, Password (= client_secret).
// Optional: Scopes.
func newOIDCTokenFetcher(auth *scenario.KafkaAuth) (func(context.Context) (oauth.Auth, error), error) {
	if auth.TokenURL == "" || auth.ClientID == "" || auth.Password == "" {
		return nil, fmt.Errorf("kafka: sasl_oauthbearer requires token_url, client_id and password (client_secret)")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	var (
		mu       sync.Mutex
		cached   string
		expiryAt time.Time
	)
	return func(ctx context.Context) (oauth.Auth, error) {
		mu.Lock()
		defer mu.Unlock()
		// Refresh skew scales with the token's TTL so a token with TTL <= 60s
		// does not land as immediately-stale on every SASL session. 60s is
		// fine for the 1h+ tokens most IdPs issue; shorter tokens keep at
		// least half their life before a refresh is triggered.
		if cached != "" && !expiryAt.IsZero() {
			skew := 60 * time.Second
			ttl := time.Until(expiryAt)
			if half := ttl / 2; half > 0 && half < skew {
				skew = half
			}
			if time.Now().Before(expiryAt.Add(-skew)) {
				return oauth.Auth{Token: cached}, nil
			}
		}
		form := url.Values{}
		form.Set("grant_type", "client_credentials")
		form.Set("client_id", auth.ClientID)
		form.Set("client_secret", auth.Password)
		if len(auth.Scopes) > 0 {
			form.Set("scope", strings.Join(auth.Scopes, " "))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, auth.TokenURL, bytes.NewBufferString(form.Encode()))
		if err != nil {
			return oauth.Auth{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return oauth.Auth{}, fmt.Errorf("kafka: oidc token fetch: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return oauth.Auth{}, fmt.Errorf("kafka: oidc token endpoint returned %d: %s", resp.StatusCode, string(body))
		}
		var out struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return oauth.Auth{}, fmt.Errorf("kafka: oidc token decode: %w", err)
		}
		if out.AccessToken == "" {
			return oauth.Auth{}, fmt.Errorf("kafka: oidc response missing access_token")
		}
		cached = out.AccessToken
		ttl := time.Duration(out.ExpiresIn) * time.Second
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		expiryAt = time.Now().Add(ttl)
		return oauth.Auth{Token: cached}, nil
	}, nil
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
