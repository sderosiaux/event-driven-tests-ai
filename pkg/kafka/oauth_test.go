package kafka

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOIDCTokenFetcherHitsTokenEndpoint(t *testing.T) {
	var (
		calls int32
		seenForm url.Values
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = r.ParseForm()
		seenForm = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-abc","expires_in":3600}`))
	}))
	defer srv.Close()

	fetcher, err := newOIDCTokenFetcher(&scenario.KafkaAuth{
		Type: scenario.KafkaAuthOAuthBearer,
		TokenURL: srv.URL,
		ClientID: "edt",
		Password: "secret",
		Scopes:   []string{"kafka", "registry"},
	})
	require.NoError(t, err)

	auth, err := fetcher(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-abc", auth.Token)
	assert.Equal(t, "client_credentials", seenForm.Get("grant_type"))
	assert.Equal(t, "edt", seenForm.Get("client_id"))
	assert.Equal(t, "secret", seenForm.Get("client_secret"))
	assert.Equal(t, "kafka registry", seenForm.Get("scope"))
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestOIDCTokenFetcherCachesUntilExpiry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer srv.Close()
	fetcher, err := newOIDCTokenFetcher(&scenario.KafkaAuth{
		Type: scenario.KafkaAuthOAuthBearer, TokenURL: srv.URL, ClientID: "x", Password: "y",
	})
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err := fetcher(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "token must be cached until near expiry")
}

func TestOIDCTokenFetcherSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()
	fetcher, err := newOIDCTokenFetcher(&scenario.KafkaAuth{
		Type: scenario.KafkaAuthOAuthBearer, TokenURL: srv.URL, ClientID: "x", Password: "y",
	})
	require.NoError(t, err)
	_, err = fetcher(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}
