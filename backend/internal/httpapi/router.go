package httpapi

import (
	"net/http"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/freeswitch"
	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/ws"

	"github.com/gin-gonic/gin"
)

func NewRouter(
	cfg config.Config,
	sessions *session.Manager,
	providers *config.ProviderStore,
	orchestrator *pipeline.Orchestrator,
	hub *ws.Hub,
	streamServer freeswitch.StreamServer,
) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery())

	engine.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, buildHealthResponse(cfg, providers.Get(), sessions.Count()))
	})

	engine.GET("/api/sessions", func(c *gin.Context) {
		c.JSON(http.StatusOK, buildSessionListResponse(sessions.List()))
	})

	engine.GET("/api/sessions/:id", func(c *gin.Context) {
		current, ok := sessions.Get(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}

		c.JSON(http.StatusOK, buildSessionDetailResponse(current, cfg.NodeName))
	})

	engine.POST("/api/settings/providers", func(c *gin.Context) {
		var payload config.Providers
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, providers.Update(payload))
	})

	engine.POST("/api/sessions/:id/interrupt", func(c *gin.Context) {
		if _, ok := sessions.Get(c.Param("id")); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}

		if err := orchestrator.Interrupt(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{"accepted": true})
	})

	engine.GET("/ws", hub.ServeWS)

	wsGroup := engine.Group("/ws")
	streamServer.RegisterRoutes(wsGroup)

	return engine
}
