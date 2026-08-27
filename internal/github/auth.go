package orchestra

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

// AppAuth holds GitHub App credentials for authentication.
type AppAuth struct {
	AppID          int64
	PrivateKeyPath string
	InstallationID int64
}

// HTTPClient abstracts HTTP requests for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// TokenCache provides cached access to GitHub App installation tokens.
type TokenCache struct {
	mu     sync.Mutex
	token  string
	expiry time.Time

	auth   *AppAuth
	client HTTPClient
	now    func() time.Time // injectable clock for testing
}

// NewTokenCache creates a cache that manages token lifecycle for the given app credentials.
func NewTokenCache(auth *AppAuth, client HTTPClient) *TokenCache {
	return &TokenCache{
		auth:   auth,
		client: client,
		now:    time.Now,
	}
}

// Token returns a valid installation token, using the cache when possible.
// Regenerates the token if it is expired or will expire within 5 minutes.
func (tc *TokenCache) Token(ctx context.Context) (string, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	now := tc.now()
	if tc.token != "" && now.Add(5*time.Minute).Before(tc.expiry) {
		return tc.token, nil
	}

	keyData, err := os.ReadFile(tc.auth.PrivateKeyPath)
	if err != nil {
		return "", fmt.Errorf("reading private key: %w", err)
	}

	jwtToken, err := GenerateJWT(tc.auth.AppID, keyData)
	if err != nil {
		return "", fmt.Errorf("generating JWT: %w", err)
	}

	token, expiry, err := GetInstallationToken(ctx, tc.client, jwtToken, tc.auth.InstallationID)
	if err != nil {
		return "", fmt.Errorf("getting installation token: %w", err)
	}

	tc.token = token
	tc.expiry = expiry
	return token, nil
}

// GenerateJWT creates an RS256-signed JWT for GitHub App authentication.
// The token has iss=appID, iat=now-60s, exp=now+10min per GitHub's spec.
func GenerateJWT(appID int64, privateKey []byte) (string, error) {
	block, _ := pem.Decode(privateKey)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block from private key")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 format as fallback.
		parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return "", fmt.Errorf("parsing private key: %w", err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("private key is not RSA")
		}
		key = rsaKey
	}

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    fmt.Sprintf("%d", appID),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// installationTokenResponse is the response from GitHub's installation token endpoint.
type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetInstallationToken exchanges a JWT for an installation access token.
func GetInstallationToken(ctx context.Context, client HTTPClient, jwtToken string, installationID int64) (string, time.Time, error) {
	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("requesting installation token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp installationTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("decoding response: %w", err)
	}

	return tokenResp.Token, tokenResp.ExpiresAt, nil
}

// ConfigureGHAuth sets up gh CLI to use the given token for git operations.
func ConfigureGHAuth(token string) error {
	cmd := exec.Command("gh", "auth", "setup-git")
	cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh auth setup-git: %s: %w", out, err)
	}
	return nil
}

// EnsureGitHubAuth sets up GitHub authentication, preferring App auth if configured,
// falling back silently to existing gh CLI auth (PAT/OAuth) otherwise.
func EnsureGitHubAuth(ctx context.Context) error {
	cfg, err := config.LoadGitHubAppConfig()
	if err != nil {
		return fmt.Errorf("loading github app config: %w", err)
	}

	// No App config — fall back to existing gh auth silently.
	if cfg == nil {
		return nil
	}

	auth := &AppAuth{
		AppID:          cfg.AppID,
		PrivateKeyPath: cfg.PrivateKeyPath,
		InstallationID: cfg.InstallationID,
	}

	cache := NewTokenCache(auth, http.DefaultClient)
	token, err := cache.Token(ctx)
	if err != nil {
		return fmt.Errorf("obtaining installation token: %w", err)
	}

	return ConfigureGHAuth(token)
}
