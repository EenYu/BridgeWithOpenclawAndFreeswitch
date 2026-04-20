package ws

import (
	"fmt"
	"net/http"
	"strings"

	"bridgewithclawandfreeswitch/backend/internal/config"
)

type AccessPolicy struct {
	EndpointName string
	AuthToken    string
	Origins      map[string]struct{}
	AllowEmpty   bool
}

func NewAccessPolicy(endpointName string, cfg config.WebSocketEndpointConfig) AccessPolicy {
	origins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		normalized := strings.TrimSpace(origin)
		if normalized == "" {
			continue
		}
		origins[strings.ToLower(normalized)] = struct{}{}
	}

	return AccessPolicy{
		EndpointName: endpointName,
		AuthToken:    strings.TrimSpace(cfg.AuthToken),
		Origins:      origins,
		AllowEmpty:   cfg.AllowEmptyOrigin,
	}
}

func (p AccessPolicy) Validate(r *http.Request) (int, error) {
	if !p.originAllowed(r.Header.Get("Origin")) {
		return http.StatusForbidden, fmt.Errorf("%s websocket origin not allowed", p.EndpointName)
	}
	if p.AuthToken == "" {
		return http.StatusOK, nil
	}
	if p.tokenAllowed(r) {
		return http.StatusOK, nil
	}
	return http.StatusUnauthorized, fmt.Errorf("%s websocket unauthorized", p.EndpointName)
}

func (p AccessPolicy) originAllowed(origin string) bool {
	if len(p.Origins) == 0 {
		return true
	}
	if strings.TrimSpace(origin) == "" {
		return p.AllowEmpty
	}
	_, ok := p.Origins[strings.ToLower(strings.TrimSpace(origin))]
	return ok
}

func (p AccessPolicy) tokenAllowed(r *http.Request) bool {
	token := p.AuthToken
	if token == "" {
		return true
	}

	candidates := []string{
		r.URL.Query().Get("access_token"),
		r.URL.Query().Get("token"),
		r.Header.Get("X-Bridge-Token"),
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader != "" {
		candidates = append(candidates,
			authHeader,
			strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")),
			strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer;")),
		)
	}

	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == token {
			return true
		}
	}
	return false
}
