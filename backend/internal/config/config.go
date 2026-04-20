package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/session"
)

const (
	defaultProviderTimeout  = 10 * time.Second
	defaultTTSSTSEndpoint   = "https://openspeech.bytedance.com/api/v1/sts/token"
	defaultOpenClawOrigin   = "http://127.0.0.1"
	defaultTTSNamespace     = "BidirectionalTTS"
	defaultVolcengineTTSRID = "volc.service_type.10029"
	defaultWSWriteTimeout   = 2 * time.Second
	defaultWSBroadcastQueue = 32
)

type Config struct {
	HTTPAddress       string
	ReadHeaderTimeout time.Duration
	Version           string
	NodeName          string
	Providers         Providers
	WebSocket         WebSocketConfig
}

type WebSocketEndpointConfig struct {
	AuthToken        string
	AllowedOrigins   []string
	AllowEmptyOrigin bool
}

type WebSocketConfig struct {
	Dashboard          WebSocketEndpointConfig
	FreeSWITCH         WebSocketEndpointConfig
	BroadcastQueueSize int
	WriteTimeout       time.Duration
}

type ProviderConfig struct {
	Vendor     string `json:"vendor"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model,omitempty"`
	Enabled    bool   `json:"enabled"`
	APIKeyHint string `json:"apiKeyHint,omitempty"`

	Transport      string        `json:"-"`
	AppKey         string        `json:"-"`
	APIKey         string        `json:"-"`
	AuthType       string        `json:"-"`
	APIKeyHeader   string        `json:"-"`
	ResourceID     string        `json:"-"`
	Cluster        string        `json:"-"`
	VoiceType      string        `json:"-"`
	AudioFormat    string        `json:"-"`
	AudioCodec     string        `json:"-"`
	Language       string        `json:"-"`
	UID            string        `json:"-"`
	Origin         string        `json:"-"`
	STSEndpoint    string        `json:"-"`
	Namespace      string        `json:"-"`
	SampleRateHz   int           `json:"-"`
	BitsPerSample  int           `json:"-"`
	EnableITN      bool          `json:"-"`
	EnablePunc     bool          `json:"-"`
	ShowUtterances bool          `json:"-"`
	Timeout        time.Duration `json:"-"`
}

type Providers struct {
	STT      ProviderConfig `json:"stt"`
	OpenClaw ProviderConfig `json:"openclaw"`
	TTS      ProviderConfig `json:"tts"`
}

func Load() Config {
	httpAddress := getenvDefault("BRIDGE_HTTP_ADDR", ":8080")
	version := getenvDefault("BRIDGE_VERSION", "0.1.0")
	nodeName := getenvDefault("BRIDGE_NODE_NAME", "bridge-node-1")

	return Config{
		HTTPAddress:       httpAddress,
		ReadHeaderTimeout: 5 * time.Second,
		Version:           version,
		NodeName:          nodeName,
		Providers: Providers{
			STT: loadProviderConfig("BRIDGE_STT", ProviderConfig{
				Vendor:         "noop-stt",
				Endpoint:       "memory://stt",
				Enabled:        true,
				APIKeyHint:     "stt-key-not-required",
				AudioFormat:    "pcm",
				AudioCodec:     "raw",
				SampleRateHz:   16000,
				BitsPerSample:  16,
				EnableITN:      true,
				EnablePunc:     true,
				ShowUtterances: true,
			}),
			OpenClaw: loadProviderConfig("BRIDGE_OPENCLAW", ProviderConfig{
				Vendor:     "echo-openclaw",
				Endpoint:   "memory://openclaw",
				Enabled:    true,
				APIKeyHint: "openclaw-key-not-required",
				Origin:     defaultOpenClawOrigin,
			}),
			TTS: loadProviderConfig("BRIDGE_TTS", ProviderConfig{
				Vendor:       "noop-tts",
				Endpoint:     "memory://tts",
				Enabled:      true,
				APIKeyHint:   "tts-key-not-required",
				Cluster:      "volcano_tts",
				AudioFormat:  "pcm",
				SampleRateHz: 16000,
				STSEndpoint:  defaultTTSSTSEndpoint,
				Namespace:    defaultTTSNamespace,
				ResourceID:   defaultVolcengineTTSRID,
			}),
		},
		WebSocket: WebSocketConfig{
			Dashboard: loadWebSocketEndpointConfig("BRIDGE_WS_DASHBOARD", WebSocketEndpointConfig{}),
			FreeSWITCH: loadWebSocketEndpointConfig("BRIDGE_WS_FREESWITCH", WebSocketEndpointConfig{
				AllowEmptyOrigin: true,
			}),
			BroadcastQueueSize: getenvIntDefault("BRIDGE_WS_BROADCAST_QUEUE_SIZE", defaultWSBroadcastQueue),
			WriteTimeout:       getenvDurationMSDefault("BRIDGE_WS_WRITE_TIMEOUT_MS", defaultWSWriteTimeout),
		},
	}
}

func loadProviderConfig(prefix string, defaults ProviderConfig) ProviderConfig {
	cfg := defaults
	cfg.Vendor = getenvDefault(prefix+"_VENDOR", defaults.Vendor)
	cfg.Endpoint = getenvDefault(prefix+"_ENDPOINT", defaults.Endpoint)
	cfg.Model = getenvDefault(prefix+"_MODEL", defaults.Model)
	cfg.Enabled = getenvBoolDefault(prefix+"_ENABLED", defaults.Enabled)
	cfg.Transport = getenvDefault(prefix+"_TRANSPORT", defaults.Transport)
	cfg.AppKey = getenvFirstNonEmpty(prefix+"_APP_ID", prefix+"_APP_KEY")
	cfg.APIKey = getenvFirstNonEmpty(prefix+"_ACCESS_TOKEN", prefix+"_API_KEY")
	cfg.APIKeyHint = loadAPIKeyHint(prefix, defaults.APIKeyHint, cfg.APIKey)
	cfg.AuthType = getenvDefault(prefix+"_AUTH_TYPE", defaultAuthType(cfg.Vendor, cfg.Endpoint))
	cfg.APIKeyHeader = getenvDefault(prefix+"_API_KEY_HEADER", defaultAPIKeyHeader(cfg.AuthType))
	cfg.ResourceID = getenvDefault(prefix+"_RESOURCE_ID", defaults.ResourceID)
	cfg.Cluster = getenvDefault(prefix+"_CLUSTER", defaults.Cluster)
	cfg.VoiceType = os.Getenv(prefix + "_VOICE_TYPE")
	cfg.AudioFormat = getenvDefault(prefix+"_AUDIO_FORMAT", defaults.AudioFormat)
	cfg.AudioCodec = getenvDefault(prefix+"_AUDIO_CODEC", defaults.AudioCodec)
	cfg.Language = os.Getenv(prefix + "_LANGUAGE")
	cfg.UID = os.Getenv(prefix + "_UID")
	cfg.Origin = getenvDefault(prefix+"_ORIGIN", defaults.Origin)
	cfg.STSEndpoint = getenvDefault(prefix+"_STS_ENDPOINT", defaults.STSEndpoint)
	cfg.Namespace = getenvDefault(prefix+"_NAMESPACE", defaults.Namespace)
	cfg.SampleRateHz = getenvIntDefault(prefix+"_SAMPLE_RATE_HZ", defaults.SampleRateHz)
	cfg.BitsPerSample = getenvIntDefault(prefix+"_BITS_PER_SAMPLE", defaults.BitsPerSample)
	cfg.EnableITN = getenvBoolDefault(prefix+"_ENABLE_ITN", defaults.EnableITN)
	cfg.EnablePunc = getenvBoolDefault(prefix+"_ENABLE_PUNCTUATION", defaults.EnablePunc)
	cfg.ShowUtterances = getenvBoolDefault(prefix+"_SHOW_UTTERANCES", defaults.ShowUtterances)
	cfg.Timeout = getenvDurationMSDefault(prefix+"_TIMEOUT_MS", defaultProviderTimeout)
	return cfg
}

func loadAPIKeyHint(prefix string, fallback string, apiKey string) string {
	if explicit := os.Getenv(prefix + "_API_KEY_HINT"); explicit != "" {
		return explicit
	}
	if apiKey == "" {
		return fallback
	}
	if len(apiKey) <= 4 {
		return "***"
	}
	return apiKey[:4] + "***"
}

func defaultAuthType(vendor string, endpoint string) string {
	switch vendor {
	case "noop-stt", "echo-openclaw", "noop-tts", "volcengine-stt-ws", "volcengine-tts-ws-v3", "openclaw-gateway-ws":
		return "none"
	case "volcengine-tts-http-v1":
		return "bearer-semicolon"
	}
	if endpoint == "" || endpoint == "memory://stt" || endpoint == "memory://openclaw" || endpoint == "memory://tts" {
		return "none"
	}
	return "bearer"
}

func defaultAPIKeyHeader(authType string) string {
	if authType == "header" {
		return "X-API-Key"
	}
	return ""
}

func loadWebSocketEndpointConfig(prefix string, defaults WebSocketEndpointConfig) WebSocketEndpointConfig {
	return WebSocketEndpointConfig{
		AuthToken:        os.Getenv(prefix + "_AUTH_TOKEN"),
		AllowedOrigins:   getenvCSV(prefix + "_ALLOWED_ORIGINS"),
		AllowEmptyOrigin: getenvBoolDefault(prefix+"_ALLOW_EMPTY_ORIGIN", defaults.AllowEmptyOrigin),
	}
}

func getenvDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func getenvFirstNonEmpty(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func getenvCSV(name string) []string {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}

	parts := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func getenvBoolDefault(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvIntDefault(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getenvDurationMSDefault(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return time.Duration(parsed) * time.Millisecond
}

type ProviderStore struct {
	mu        sync.RWMutex
	providers Providers
}

func NewProviderStore(initial Providers) *ProviderStore {
	return &ProviderStore{providers: initial}
}

func (s *ProviderStore) Get() Providers {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.providers
}

func (s *ProviderStore) Update(next Providers) Providers {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 通过 API 更新可见配置时，保留进程启动时注入的鉴权、Origin 与私有协商参数，
	// 避免控制面刷新后把运行时连接能力覆盖掉。
	next.STT = mergeRuntimeFields(s.providers.STT, next.STT)
	next.OpenClaw = mergeRuntimeFields(s.providers.OpenClaw, next.OpenClaw)
	next.TTS = mergeRuntimeFields(s.providers.TTS, next.TTS)

	s.providers = next
	return s.providers
}

func mergeRuntimeFields(current ProviderConfig, next ProviderConfig) ProviderConfig {
	next.Transport = current.Transport
	next.AppKey = current.AppKey
	next.APIKey = current.APIKey
	next.AuthType = current.AuthType
	next.APIKeyHeader = current.APIKeyHeader
	next.ResourceID = current.ResourceID
	next.Cluster = current.Cluster
	next.VoiceType = current.VoiceType
	next.AudioFormat = current.AudioFormat
	next.AudioCodec = current.AudioCodec
	next.Language = current.Language
	next.UID = current.UID
	next.Origin = current.Origin
	next.STSEndpoint = current.STSEndpoint
	next.Namespace = current.Namespace
	next.SampleRateHz = current.SampleRateHz
	next.BitsPerSample = current.BitsPerSample
	next.EnableITN = current.EnableITN
	next.EnablePunc = current.EnablePunc
	next.ShowUtterances = current.ShowUtterances
	next.Timeout = current.Timeout
	if next.APIKeyHint == "" {
		next.APIKeyHint = current.APIKeyHint
	}
	return next
}

func (s *ProviderStore) SessionBindings() session.ProviderBindings {
	current := s.Get()
	return session.ProviderBindings{
		STT:      current.STT.Vendor,
		OpenClaw: current.OpenClaw.Vendor,
		TTS:      current.TTS.Vendor,
	}
}
