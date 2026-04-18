export interface ProviderConfig {
  vendor: string;
  endpoint: string;
  model: string;
  apiKeyHint: string;
  enabled: boolean;
}

export interface ProviderSettings {
  stt: ProviderConfig;
  openclaw: ProviderConfig;
  tts: ProviderConfig;
}
