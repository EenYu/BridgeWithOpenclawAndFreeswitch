package config

import "testing"

func TestLoadReadsProviderHTTPSettingsFromEnv(t *testing.T) {
	t.Setenv("BRIDGE_STT_VENDOR", "volcengine-stt-ws")
	t.Setenv("BRIDGE_STT_ENDPOINT", "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async")
	t.Setenv("BRIDGE_STT_MODEL", "general")
	t.Setenv("BRIDGE_STT_TRANSPORT", "volcengine-binary-ws")
	t.Setenv("BRIDGE_STT_APP_ID", "volc-app-id")
	t.Setenv("BRIDGE_STT_ACCESS_TOKEN", "stt-secret")
	t.Setenv("BRIDGE_STT_RESOURCE_ID", "volc.seedasr.sauc.duration")
	t.Setenv("BRIDGE_STT_AUDIO_FORMAT", "pcm")
	t.Setenv("BRIDGE_STT_AUDIO_CODEC", "raw")
	t.Setenv("BRIDGE_STT_UID", "bridge-fs1")
	t.Setenv("BRIDGE_STT_SAMPLE_RATE_HZ", "16000")
	t.Setenv("BRIDGE_STT_BITS_PER_SAMPLE", "16")
	t.Setenv("BRIDGE_STT_ENABLE_ITN", "true")
	t.Setenv("BRIDGE_STT_ENABLE_PUNCTUATION", "true")
	t.Setenv("BRIDGE_STT_SHOW_UTTERANCES", "true")
	t.Setenv("BRIDGE_STT_TIMEOUT_MS", "2500")

	cfg := Load()

	if cfg.Providers.STT.Vendor != "volcengine-stt-ws" {
		t.Fatalf("unexpected stt vendor %q", cfg.Providers.STT.Vendor)
	}
	if cfg.Providers.STT.Endpoint != "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async" {
		t.Fatalf("unexpected stt endpoint %q", cfg.Providers.STT.Endpoint)
	}
	if cfg.Providers.STT.Model != "general" {
		t.Fatalf("unexpected stt model %q", cfg.Providers.STT.Model)
	}
	if cfg.Providers.STT.Transport != "volcengine-binary-ws" {
		t.Fatalf("unexpected stt transport %q", cfg.Providers.STT.Transport)
	}
	if cfg.Providers.STT.AppKey != "volc-app-id" {
		t.Fatalf("unexpected stt app key %q", cfg.Providers.STT.AppKey)
	}
	if cfg.Providers.STT.APIKey != "stt-secret" {
		t.Fatalf("unexpected stt api key %q", cfg.Providers.STT.APIKey)
	}
	if cfg.Providers.STT.ResourceID != "volc.seedasr.sauc.duration" {
		t.Fatalf("unexpected stt resource id %q", cfg.Providers.STT.ResourceID)
	}
	if cfg.Providers.STT.AudioFormat != "pcm" || cfg.Providers.STT.AudioCodec != "raw" {
		t.Fatalf("unexpected stt audio config %+v", cfg.Providers.STT)
	}
	if cfg.Providers.STT.UID != "bridge-fs1" {
		t.Fatalf("unexpected stt uid %q", cfg.Providers.STT.UID)
	}
	if cfg.Providers.STT.SampleRateHz != 16000 || cfg.Providers.STT.BitsPerSample != 16 {
		t.Fatalf("unexpected stt pcm settings %+v", cfg.Providers.STT)
	}
	if !cfg.Providers.STT.EnableITN || !cfg.Providers.STT.EnablePunc || !cfg.Providers.STT.ShowUtterances {
		t.Fatalf("unexpected stt booleans %+v", cfg.Providers.STT)
	}
	if cfg.Providers.STT.Timeout.Milliseconds() != 2500 {
		t.Fatalf("unexpected stt timeout %s", cfg.Providers.STT.Timeout)
	}
}

func TestLoadSupportsLegacyProviderCredentialNames(t *testing.T) {
	t.Setenv("BRIDGE_STT_APP_KEY", "legacy-app-key")
	t.Setenv("BRIDGE_STT_API_KEY", "legacy-api-key")

	cfg := Load()

	if cfg.Providers.STT.AppKey != "legacy-app-key" {
		t.Fatalf("unexpected legacy stt app key %q", cfg.Providers.STT.AppKey)
	}
	if cfg.Providers.STT.APIKey != "legacy-api-key" {
		t.Fatalf("unexpected legacy stt api key %q", cfg.Providers.STT.APIKey)
	}
}

func TestProviderStoreUpdatePreservesRuntimeSecrets(t *testing.T) {
	store := NewProviderStore(Providers{
		STT: ProviderConfig{
			Vendor:         "volcengine-stt-ws",
			Endpoint:       "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async",
			Enabled:        true,
			Transport:      "volcengine-binary-ws",
			AppKey:         "volc-app-id",
			APIKey:         "stt-secret",
			ResourceID:     "volc.seedasr.sauc.duration",
			AudioFormat:    "pcm",
			AudioCodec:     "raw",
			UID:            "bridge-fs1",
			SampleRateHz:   16000,
			BitsPerSample:  16,
			EnableITN:      true,
			EnablePunc:     true,
			ShowUtterances: true,
		},
	})

	updated := store.Update(Providers{
		STT: ProviderConfig{
			Vendor:   "volcengine-stt-ws",
			Endpoint: "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel",
			Enabled:  true,
		},
	})

	if updated.STT.APIKey != "stt-secret" {
		t.Fatalf("expected api key to be preserved, got %q", updated.STT.APIKey)
	}
	if updated.STT.Transport != "volcengine-binary-ws" {
		t.Fatalf("expected transport to be preserved, got %q", updated.STT.Transport)
	}
	if updated.STT.AppKey != "volc-app-id" || updated.STT.ResourceID != "volc.seedasr.sauc.duration" {
		t.Fatalf("expected volcengine credentials to be preserved, got %+v", updated.STT)
	}
	if updated.STT.UID != "bridge-fs1" || updated.STT.SampleRateHz != 16000 || updated.STT.BitsPerSample != 16 {
		t.Fatalf("expected audio settings to be preserved, got %+v", updated.STT)
	}
}
