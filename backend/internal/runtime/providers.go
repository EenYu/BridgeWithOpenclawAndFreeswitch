package runtime

import (
	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/openclaw"
	"bridgewithclawandfreeswitch/backend/internal/stt"
	"bridgewithclawandfreeswitch/backend/internal/tts"
)

func BuildProviderClients(providers config.Providers) (stt.Client, openclaw.Client, tts.Client) {
	sttClient := stt.NewClient(providers.STT)
	openClawClient := openclaw.NewClient(providers.OpenClaw)
	ttsClient := tts.NewClient(providers.TTS)
	return sttClient, openClawClient, ttsClient
}
