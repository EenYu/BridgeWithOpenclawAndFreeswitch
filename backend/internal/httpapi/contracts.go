package httpapi

import (
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/contract"
	"bridgewithclawandfreeswitch/backend/internal/session"
)

func buildHealthResponse(cfg config.Config, providers config.Providers, activeSessions int) contract.HealthResponse {
	return contract.BuildHealthResponse(cfg, providers, activeSessions, time.Now().UTC())
}

func buildSessionListResponse(items []*session.Session) []contract.SessionSummary {
	response := make([]contract.SessionSummary, 0, len(items))
	for _, item := range items {
		response = append(response, contract.SessionSummaryFromSession(item))
	}
	return response
}

func buildSessionDetailResponse(current *session.Session, nodeName string) contract.SessionDetail {
	return contract.SessionDetailFromSession(current, nodeName)
}
