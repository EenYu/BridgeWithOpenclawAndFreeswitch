package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/freeswitch"
	"bridgewithclawandfreeswitch/backend/internal/httpapi"
	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/runtime"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/stt"
	"bridgewithclawandfreeswitch/backend/internal/ws"
)

func main() {
	cfg := config.Load()
	sessionManager := session.NewManager()
	providerStore := config.NewProviderStore(cfg.Providers)
	hub := ws.NewHub(
		ws.NewAccessPolicy("dashboard", cfg.WebSocket.Dashboard),
		cfg.WebSocket.BroadcastQueueSize,
		cfg.WebSocket.WriteTimeout,
	)
	streamServer := freeswitch.NewWebSocketStreamServer(
		nil,
		ws.NewAccessPolicy("freeswitch", cfg.WebSocket.FreeSWITCH),
	)
	sttClient, openClawClient, ttsClient := runtime.BuildProviderClients(cfg.Providers)

	orchestrator := pipeline.NewOrchestrator(
		sessionManager,
		sttClient,
		openClawClient,
		ttsClient,
		streamServer,
		hub,
		providerStore,
	)
	bindTranscriptHandler(sttClient, orchestrator)

	streamServer.SetHandler(orchestrator)
	engine := httpapi.NewRouter(cfg, sessionManager, providerStore, orchestrator, hub, streamServer)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           engine,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("bridge server listening on %s", cfg.HTTPAddress)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func bindTranscriptHandler(client stt.Client, orchestrator *pipeline.Orchestrator) {
	stt.AttachTranscriptHandler(client, func(ctx context.Context, sessionID string, transcript string, final bool) error {
		if final {
			return orchestrator.HandleTranscriptFinal(ctx, sessionID, transcript)
		}
		return orchestrator.HandleTranscriptPartial(ctx, sessionID, transcript)
	})
}
