package contract

import (
	"fmt"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/session"
)

type ServiceStatus string

const (
	ServiceStatusOK       ServiceStatus = "ok"
	ServiceStatusDegraded ServiceStatus = "degraded"
	ServiceStatusError    ServiceStatus = "error"
)

type ServiceHealth struct {
	Name      string        `json:"name"`
	Status    ServiceStatus `json:"status"`
	Detail    string        `json:"detail"`
	LatencyMs *int          `json:"latencyMs,omitempty"`
}

type HealthResponse struct {
	Status         ServiceStatus   `json:"status"`
	Version        string          `json:"version"`
	CheckedAt      time.Time       `json:"checkedAt"`
	ActiveSessions int             `json:"activeSessions"`
	Services       []ServiceHealth `json:"services"`
}

type SessionSummary struct {
	ID             string               `json:"id"`
	CallID         string               `json:"callId"`
	State          session.SessionState `json:"state"`
	Caller         string               `json:"caller"`
	StartedAt      time.Time            `json:"startedAt"`
	LastTranscript string               `json:"lastTranscript,omitempty"`
}

type TranscriptEntry struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

type ProviderLatency struct {
	Provider  string    `json:"provider"`
	LatencyMs int       `json:"latencyMs"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SessionLogEntry struct {
	ID        string    `json:"id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
}

type ProviderStatus struct {
	STT      string `json:"stt"`
	OpenClaw string `json:"openclaw"`
	TTS      string `json:"tts"`
}

type SessionDetail struct {
	SessionSummary
	UpdatedAt         time.Time         `json:"updatedAt"`
	ClosedAt          *time.Time        `json:"closedAt,omitempty"`
	BridgeNode        string            `json:"bridgeNode"`
	ProviderStatus    ProviderStatus    `json:"providerStatus"`
	PlaybackActive    bool              `json:"playbackActive"`
	Transcripts       []TranscriptEntry `json:"transcripts"`
	RecentLogs        []SessionLogEntry `json:"recentLogs"`
	ProviderLatencies []ProviderLatency `json:"providerLatencies"`
}

func BuildHealthResponse(cfg config.Config, providers config.Providers, activeSessions int, checkedAt time.Time) HealthResponse {
	services := []ServiceHealth{
		{
			Name:   "bridge",
			Status: ServiceStatusOK,
			Detail: fmt.Sprintf("%s on %s", cfg.NodeName, cfg.HTTPAddress),
		},
		buildProviderHealth("stt", providers.STT),
		buildProviderHealth("openclaw", providers.OpenClaw),
		buildProviderHealth("tts", providers.TTS),
	}

	status := ServiceStatusOK
	for _, service := range services {
		if service.Status == ServiceStatusError {
			status = ServiceStatusError
			break
		}
		if service.Status == ServiceStatusDegraded {
			status = ServiceStatusDegraded
		}
	}

	return HealthResponse{
		Status:         status,
		Version:        cfg.Version,
		CheckedAt:      checkedAt,
		ActiveSessions: activeSessions,
		Services:       services,
	}
}

func SessionSummaryFromSession(current *session.Session) SessionSummary {
	return SessionSummary{
		ID:             current.ID,
		CallID:         current.CallID,
		State:          current.State,
		Caller:         current.Caller,
		StartedAt:      current.StartedAt,
		LastTranscript: current.LastTranscript,
	}
}

func SessionDetailFromSession(current *session.Session, nodeName string) SessionDetail {
	return SessionDetail{
		SessionSummary: SessionSummaryFromSession(current),
		UpdatedAt:      current.UpdatedAt,
		ClosedAt:       current.ClosedAt,
		BridgeNode:     nodeName,
		ProviderStatus: ProviderStatus{
			STT:      current.Providers.STT,
			OpenClaw: current.Providers.OpenClaw,
			TTS:      current.Providers.TTS,
		},
		PlaybackActive:    current.State == session.StateSpeaking,
		Transcripts:       []TranscriptEntry{},
		RecentLogs:        []SessionLogEntry{},
		ProviderLatencies: []ProviderLatency{},
	}
}

func TranscriptEntryForSession(sessionID string, kind string, text string, createdAt time.Time) TranscriptEntry {
	return TranscriptEntry{
		ID:        fmt.Sprintf("%s-%d", sessionID, createdAt.UnixNano()),
		Text:      text,
		Kind:      kind,
		CreatedAt: createdAt,
	}
}

func buildProviderHealth(name string, provider config.ProviderConfig) ServiceHealth {
	status := ServiceStatusOK
	detail := fmt.Sprintf("%s @ %s", provider.Vendor, provider.Endpoint)
	if provider.Vendor == "" && provider.Endpoint == "" {
		status = ServiceStatusDegraded
		detail = "not configured"
	}
	if !provider.Enabled {
		status = ServiceStatusDegraded
		detail = "disabled"
	}
	if provider.Enabled && provider.Endpoint == "" {
		status = ServiceStatusDegraded
		detail = fmt.Sprintf("%s endpoint missing", name)
	}
	if provider.Enabled && provider.AuthType != "none" && provider.APIKey == "" {
		status = ServiceStatusDegraded
		detail = fmt.Sprintf("%s auth missing", name)
	}

	return ServiceHealth{
		Name:   name,
		Status: status,
		Detail: detail,
	}
}
