package jwks

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type Client struct {
	mu        sync.RWMutex
	publicKey *rsa.PublicKey
	jwksURL   string
}

func NewClient(jwksURL string) (*Client, error) {
	client := &Client{jwksURL: jwksURL}

	if err := client.Fetch(); err != nil {
		return nil, fmt.Errorf("Failed to fetch JWKS on startup: %w", err)
	}

	// refresh every hour in background
	go client.RefreshLoop()

	return client, nil
}

func (client *Client) PublicKey() *rsa.PublicKey {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.publicKey
}

func (client *Client) Fetch() error {
	resp, err := http.Get(client.jwksURL)
	if err != nil {
		return fmt.Errorf("Failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var set jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("Failed to decode JWKS: %w", err)
	}

	if len(set.Keys) == 0 {
		return fmt.Errorf("No keys found in JWKS response")
	}

	key := set.Keys[0]

	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return fmt.Errorf("Failed to decode modulus: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return fmt.Errorf("Failed to decode exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	pub := &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}

	client.mu.Lock()
	client.publicKey = pub
	client.mu.Unlock()

	return nil
}

func (client *Client) RefreshLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if err := client.Fetch(); err != nil {
			fmt.Printf("Failed to refresh JWKS: %v\n", err)
		}
	}
}
