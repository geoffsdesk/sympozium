// Package gcpauth provides GCP authentication helpers for Sympozium components.
// It supports Workload Identity Federation for keyless authentication on GKE,
// as well as fallback to service account keys and API keys.
package gcpauth

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// TokenSource provides GCP access tokens with automatic refresh.
// It supports multiple authentication methods in priority order:
// 1. Workload Identity Federation (GKE pods)
// 2. GOOGLE_APPLICATION_CREDENTIALS (service account key file)
// 3. Application Default Credentials
type TokenSource struct {
	mu          sync.Mutex
	tokenSource oauth2.TokenSource
	projectID   string
}

// NewTokenSource creates a new GCP token source using the best available method.
// It automatically detects if running on GKE with Workload Identity.
func NewTokenSource(ctx context.Context, scopes ...string) (*TokenSource, error) {
	if len(scopes) == 0 {
		scopes = []string{
			"https://www.googleapis.com/auth/cloud-platform",
		}
	}

	creds, err := google.FindDefaultCredentials(ctx, scopes...)
	if err != nil {
		return nil, fmt.Errorf("finding GCP credentials: %w", err)
	}

	projectID := creds.ProjectID
	if p := os.Getenv("GCP_PROJECT_ID"); p != "" {
		projectID = p
	}

	return &TokenSource{
		tokenSource: creds.TokenSource,
		projectID:   projectID,
	}, nil
}

// Token returns a valid GCP access token, refreshing if needed.
func (ts *TokenSource) Token() (*oauth2.Token, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.tokenSource.Token()
}

// ProjectID returns the detected or configured GCP project ID.
func (ts *TokenSource) ProjectID() string {
	return ts.projectID
}

// GetAccessToken returns just the access token string for use in HTTP headers.
func (ts *TokenSource) GetAccessToken() (string, error) {
	token, err := ts.Token()
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

// IsWorkloadIdentity returns true if running with GKE Workload Identity.
func IsWorkloadIdentity() bool {
	// GKE Workload Identity sets this metadata server endpoint
	_, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return false
	}
	// Check for GKE metadata
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// AuthMethod describes how the current process is authenticated.
type AuthMethod string

const (
	AuthWorkloadIdentity   AuthMethod = "workload-identity"
	AuthServiceAccountKey  AuthMethod = "service-account-key"
	AuthApplicationDefault AuthMethod = "application-default"
	AuthAPIKey             AuthMethod = "api-key"
)

// DetectAuthMethod returns the authentication method in use.
func DetectAuthMethod() AuthMethod {
	if IsWorkloadIdentity() {
		return AuthWorkloadIdentity
	}
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" {
		return AuthServiceAccountKey
	}
	if os.Getenv("GOOGLE_API_KEY") != "" || os.Getenv("VERTEX_AI_API_KEY") != "" {
		return AuthAPIKey
	}
	return AuthApplicationDefault
}

// ResolveAPIKey returns the Gemini/Vertex AI API key from environment.
// Returns empty string if using token-based auth instead.
func ResolveAPIKey() string {
	if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("VERTEX_AI_API_KEY"); key != "" {
		return key
	}
	return ""
}

// VertexAIEndpoint returns the appropriate Vertex AI endpoint URL.
func VertexAIEndpoint(projectID, location, model string) string {
	if location == "" {
		location = "us-central1"
	}
	return fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		location, projectID, location, model,
	)
}

// GeminiAPIEndpoint returns the Gemini API endpoint for API key auth.
func GeminiAPIEndpoint(model string) string {
	return fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)
}

// CacheableTokenSource wraps a TokenSource with local caching to reduce
// metadata server calls in high-throughput scenarios.
type CacheableTokenSource struct {
	inner       *TokenSource
	cachedToken *oauth2.Token
	mu          sync.Mutex
	buffer      time.Duration // refresh this much before expiry
}

// NewCacheableTokenSource creates a token source that caches tokens locally.
func NewCacheableTokenSource(inner *TokenSource, buffer time.Duration) *CacheableTokenSource {
	if buffer == 0 {
		buffer = 5 * time.Minute
	}
	return &CacheableTokenSource{
		inner:  inner,
		buffer: buffer,
	}
}

// GetAccessToken returns a cached access token, refreshing if needed.
func (c *CacheableTokenSource) GetAccessToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != nil && c.cachedToken.Valid() && time.Until(c.cachedToken.Expiry) > c.buffer {
		return c.cachedToken.AccessToken, nil
	}

	token, err := c.inner.Token()
	if err != nil {
		return "", err
	}
	c.cachedToken = token
	return token.AccessToken, nil
}
