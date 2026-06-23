package keystone

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
)

type Client struct {
	cfg        config.KeystoneConfig
	baseURL    *url.URL
	httpClient *http.Client

	mu           sync.RWMutex
	serviceToken cachedServiceToken
	tokenCache   map[string]gnocchi.Context
}

type cachedServiceToken struct {
	Token     string
	ExpiresAt time.Time
}

type authRequest struct {
	Auth struct {
		Identity struct {
			Methods  []string `json:"methods"`
			Password struct {
				User struct {
					Name     string `json:"name"`
					Password string `json:"password"`
					Domain   struct {
						Name string `json:"name"`
					} `json:"domain"`
				} `json:"user"`
			} `json:"password"`
		} `json:"identity"`
		Scope struct {
			Project struct {
				Name   string `json:"name"`
				Domain struct {
					Name string `json:"name"`
				} `json:"domain"`
			} `json:"project"`
		} `json:"scope"`
	} `json:"auth"`
}

type tokenEnvelope struct {
	Token struct {
		ExpiresAt time.Time `json:"expires_at"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
		Roles []struct {
			Name string `json:"name"`
		} `json:"roles"`
	} `json:"token"`
}

func New(cfg config.KeystoneConfig) (*Client, error) {
	parsed, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return nil, fmt.Errorf("parse keystone url: %w", err)
	}
	return &Client{
		cfg:     cfg,
		baseURL: parsed,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify},
			},
		},
		tokenCache: map[string]gnocchi.Context{},
	}, nil
}

func (c *Client) ValidateToken(ctx context.Context, subjectToken string) (gnocchi.Context, error) {
	c.mu.RLock()
	if cached, ok := c.tokenCache[subjectToken]; ok {
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	serviceToken, err := c.serviceAuthToken(ctx)
	if err != nil {
		return gnocchi.Context{}, err
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/v3/auth/tokens"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return gnocchi.Context{}, err
	}
	req.Header.Set("X-Auth-Token", serviceToken)
	req.Header.Set("X-Subject-Token", subjectToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return gnocchi.Context{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return gnocchi.Context{}, fmt.Errorf("keystone token validation failed: %s", resp.Status)
	}

	var envelope tokenEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return gnocchi.Context{}, fmt.Errorf("decode keystone validation: %w", err)
	}

	authCtx := gnocchi.Context{
		Token:     subjectToken,
		UserID:    envelope.Token.User.ID,
		ProjectID: envelope.Token.Project.ID,
		Roles:     make([]string, 0, len(envelope.Token.Roles)),
	}
	for _, role := range envelope.Token.Roles {
		authCtx.Roles = append(authCtx.Roles, role.Name)
		if containsFold(c.cfg.AdminRoles, role.Name) {
			authCtx.IsAdmin = true
		}
	}

	ttl := time.Until(envelope.Token.ExpiresAt)
	if ttl > 0 {
		c.mu.Lock()
		c.tokenCache[subjectToken] = authCtx
		c.mu.Unlock()
	}

	return authCtx, nil
}

func (c *Client) serviceAuthToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	cached := c.serviceToken
	c.mu.RUnlock()
	if cached.Token != "" && time.Until(cached.ExpiresAt) > c.cfg.ServiceTokenSkew {
		return cached.Token, nil
	}

	reqBody := authRequest{}
	reqBody.Auth.Identity.Methods = []string{"password"}
	reqBody.Auth.Identity.Password.User.Name = c.cfg.Username
	reqBody.Auth.Identity.Password.User.Password = c.cfg.Password
	reqBody.Auth.Identity.Password.User.Domain.Name = c.cfg.UserDomainName
	reqBody.Auth.Scope.Project.Name = c.cfg.ProjectName
	reqBody.Auth.Scope.Project.Domain.Name = c.cfg.ProjectDomainName

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/v3/auth/tokens"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keystone service auth failed: %s", resp.Status)
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("keystone service auth response missing X-Subject-Token")
	}

	var envelope tokenEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "", fmt.Errorf("decode keystone auth: %w", err)
	}

	c.mu.Lock()
	c.serviceToken = cachedServiceToken{
		Token:     token,
		ExpiresAt: envelope.Token.ExpiresAt,
	}
	c.mu.Unlock()

	return token, nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
