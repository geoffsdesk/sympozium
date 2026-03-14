package gcpauth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

// SecretResolver resolves secrets from Google Cloud Secret Manager.
// It provides a caching layer to reduce API calls and supports
// automatic version resolution.
type SecretResolver struct {
	client    *secretmanager.Client
	projectID string
	cache     map[string]*cachedSecret
	mu        sync.RWMutex
	cacheTTL  time.Duration
}

type cachedSecret struct {
	value     string
	fetchedAt time.Time
}

// NewSecretResolver creates a new Secret Manager resolver.
func NewSecretResolver(ctx context.Context, projectID string) (*SecretResolver, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating secret manager client: %w", err)
	}

	return &SecretResolver{
		client:    client,
		projectID: projectID,
		cache:     make(map[string]*cachedSecret),
		cacheTTL:  5 * time.Minute,
	}, nil
}

// GetSecret retrieves a secret value from Secret Manager.
// The secretRef can be:
//   - "secret-name" (resolves to projects/{project}/secrets/{name}/versions/latest)
//   - "projects/{project}/secrets/{name}/versions/{version}" (fully qualified)
//   - "sm://secret-name" (explicit Secret Manager prefix)
func (sr *SecretResolver) GetSecret(ctx context.Context, secretRef string) (string, error) {
	// Check cache first
	sr.mu.RLock()
	if cached, ok := sr.cache[secretRef]; ok && time.Since(cached.fetchedAt) < sr.cacheTTL {
		sr.mu.RUnlock()
		return cached.value, nil
	}
	sr.mu.RUnlock()

	// Build the resource name
	name := sr.resolveSecretName(secretRef)

	result, err := sr.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	})
	if err != nil {
		return "", fmt.Errorf("accessing secret %s: %w", secretRef, err)
	}

	value := string(result.Payload.Data)

	// Update cache
	sr.mu.Lock()
	sr.cache[secretRef] = &cachedSecret{
		value:     value,
		fetchedAt: time.Now(),
	}
	sr.mu.Unlock()

	return value, nil
}

// resolveSecretName converts a short secret reference to a fully qualified name.
func (sr *SecretResolver) resolveSecretName(ref string) string {
	// Strip sm:// prefix if present
	ref = strings.TrimPrefix(ref, "sm://")

	// Already fully qualified
	if strings.HasPrefix(ref, "projects/") {
		return ref
	}

	// Short name — resolve to latest version
	return fmt.Sprintf("projects/%s/secrets/%s/versions/latest", sr.projectID, ref)
}

// IsSecretManagerRef returns true if the reference points to Secret Manager
// (as opposed to a Kubernetes Secret).
func IsSecretManagerRef(ref string) bool {
	return strings.HasPrefix(ref, "sm://") ||
		strings.HasPrefix(ref, "projects/") ||
		strings.HasPrefix(ref, "gcsm://")
}

// Close closes the Secret Manager client.
func (sr *SecretResolver) Close() error {
	return sr.client.Close()
}

// CreateSecret creates a new secret in Secret Manager.
func (sr *SecretResolver) CreateSecret(ctx context.Context, secretID string, value []byte) error {
	// Create the secret
	secret, err := sr.client.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   fmt.Sprintf("projects/%s", sr.projectID),
		SecretId: secretID,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating secret: %w", err)
	}

	// Add the secret version
	_, err = sr.client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent: secret.Name,
		Payload: &secretmanagerpb.SecretPayload{
			Data: value,
		},
	})
	if err != nil {
		return fmt.Errorf("adding secret version: %w", err)
	}

	return nil
}
