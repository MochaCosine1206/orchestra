package orchestra

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, pemBytes
}

func TestGenerateJWT(t *testing.T) {
	t.Parallel()
	key, pemBytes := generateTestKey(t)

	t.Run("valid JWT with correct claims", func(t *testing.T) {
		t.Parallel()
		tokenStr, err := GenerateJWT(12345, pemBytes)
		if err != nil {
			t.Fatalf("GenerateJWT: %v", err)
		}

		parsed, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return &key.PublicKey, nil
		})
		if err != nil {
			t.Fatalf("parsing JWT: %v", err)
		}

		claims := parsed.Claims.(*jwt.RegisteredClaims)
		if claims.Issuer != "12345" {
			t.Errorf("issuer = %q, want %q", claims.Issuer, "12345")
		}

		now := time.Now()
		iat := claims.IssuedAt.Time
		exp := claims.ExpiresAt.Time

		// iat should be ~60s in the past
		if diff := now.Sub(iat); diff < 55*time.Second || diff > 65*time.Second {
			t.Errorf("iat drift = %v, want ~60s", diff)
		}
		// exp should be ~10min in the future
		if diff := exp.Sub(now); diff < 9*time.Minute || diff > 11*time.Minute {
			t.Errorf("exp drift = %v, want ~10min", diff)
		}
	})

	t.Run("invalid PEM returns error", func(t *testing.T) {
		t.Parallel()
		_, err := GenerateJWT(1, []byte("not-a-pem"))
		if err == nil {
			t.Fatal("expected error for invalid PEM")
		}
	})

	t.Run("PKCS8 key format", func(t *testing.T) {
		t.Parallel()
		pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("marshalling PKCS8: %v", err)
		}
		pkcs8PEM := pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: pkcs8Bytes,
		})

		tokenStr, err := GenerateJWT(99, pkcs8PEM)
		if err != nil {
			t.Fatalf("GenerateJWT with PKCS8: %v", err)
		}
		if tokenStr == "" {
			t.Error("expected non-empty token")
		}
	})
}

// mockHTTPClient implements HTTPClient for testing.
type mockHTTPClient struct {
	doFunc func(*http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func TestGetInstallationToken(t *testing.T) {
	t.Parallel()

	t.Run("successful token exchange", func(t *testing.T) {
		t.Parallel()
		expiry := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
		respBody := fmt.Sprintf(`{"token":"ghs_test123","expires_at":"%s"}`, expiry.Format(time.RFC3339))

		client := &mockHTTPClient{doFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", req.Method)
			}
			if auth := req.Header.Get("Authorization"); auth != "Bearer fake-jwt" {
				t.Errorf("auth = %q, want %q", auth, "Bearer fake-jwt")
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewBufferString(respBody)),
			}, nil
		}}

		token, exp, err := GetInstallationToken(context.Background(), client, "fake-jwt", 42)
		if err != nil {
			t.Fatalf("GetInstallationToken: %v", err)
		}
		if token != "ghs_test123" {
			t.Errorf("token = %q, want %q", token, "ghs_test123")
		}
		if !exp.Equal(expiry) {
			t.Errorf("expiry = %v, want %v", exp, expiry)
		}
	})

	t.Run("non-201 status returns error", func(t *testing.T) {
		t.Parallel()
		client := &mockHTTPClient{doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(bytes.NewBufferString(`{"message":"bad credentials"}`)),
			}, nil
		}}

		_, _, err := GetInstallationToken(context.Background(), client, "bad-jwt", 1)
		if err == nil {
			t.Fatal("expected error for 401 response")
		}
	})
}

func TestTokenCache(t *testing.T) {
	t.Parallel()

	// Write a test key to a temp file.
	_, pemBytes := generateTestKey(t)
	keyFile := filepath.Join(t.TempDir(), "test-key.pem")
	if err := os.WriteFile(keyFile, pemBytes, 0o600); err != nil {
		t.Fatalf("writing key file: %v", err)
	}

	auth := &AppAuth{
		AppID:          1,
		PrivateKeyPath: keyFile,
		InstallationID: 100,
	}

	t.Run("returns cached token when valid", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		expiry := time.Now().Add(1 * time.Hour).UTC()
		respBody, _ := json.Marshal(installationTokenResponse{
			Token:     "ghs_cached",
			ExpiresAt: expiry,
		})

		client := &mockHTTPClient{doFunc: func(req *http.Request) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}, nil
		}}

		cache := NewTokenCache(auth, client)
		ctx := context.Background()

		tok1, err := cache.Token(ctx)
		if err != nil {
			t.Fatalf("first Token call: %v", err)
		}
		tok2, err := cache.Token(ctx)
		if err != nil {
			t.Fatalf("second Token call: %v", err)
		}

		if tok1 != tok2 {
			t.Errorf("tokens differ: %q vs %q", tok1, tok2)
		}
		if callCount != 1 {
			t.Errorf("HTTP calls = %d, want 1 (cached)", callCount)
		}
	})

	t.Run("regenerates when within 5-minute buffer", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		client := &mockHTTPClient{doFunc: func(req *http.Request) (*http.Response, error) {
			callCount++
			respBody, _ := json.Marshal(installationTokenResponse{
				Token:     fmt.Sprintf("ghs_call_%d", callCount),
				ExpiresAt: time.Now().Add(1 * time.Hour).UTC(),
			})
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}, nil
		}}

		cache := NewTokenCache(auth, client)
		// Pre-populate with a token that expires in 3 minutes (within 5-min buffer).
		cache.token = "ghs_expiring_soon"
		cache.expiry = time.Now().Add(3 * time.Minute)

		tok, err := cache.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok == "ghs_expiring_soon" {
			t.Error("should have regenerated, not returned expiring token")
		}
		if callCount != 1 {
			t.Errorf("HTTP calls = %d, want 1", callCount)
		}
	})
}

func TestEnsureGitHubAuth_Fallback(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.

	// Point config to a temp dir with no github-app.json.
	tmpDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmpDir)

	err := EnsureGitHubAuth(context.Background())
	if err != nil {
		t.Fatalf("EnsureGitHubAuth should succeed when no config exists: %v", err)
	}
}
