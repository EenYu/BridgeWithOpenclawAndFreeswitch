import type { ProviderSettings } from "../types/providers";

interface ProviderConfigPanelProps {
  settings: ProviderSettings;
  onChange: (next: ProviderSettings) => void;
  onSubmit?: (settings: ProviderSettings) => Promise<void> | void;
  submitLabel?: string;
}

function ProviderConfigPanel({
  settings,
  onChange,
  onSubmit,
  submitLabel = "Save provider settings",
}: ProviderConfigPanelProps) {
  const updateProvider = (
    provider: keyof ProviderSettings,
    field: keyof ProviderSettings[keyof ProviderSettings],
    value: string | boolean,
  ) => {
    onChange({
      ...settings,
      [provider]: {
        ...settings[provider],
        [field]: value,
      },
    });
  };

  return (
    <section className="surface">
      <header className="surface-header">
        <div>
          <h3>Provider wiring</h3>
          <p>统一收口 STT、OpenClaw 和 TTS 的基础连接配置。</p>
        </div>
      </header>

      <form
        onSubmit={(event) => {
          event.preventDefault();
          void onSubmit?.(settings);
        }}
      >
        <div className="section-stack">
          {(Object.keys(settings) as Array<keyof ProviderSettings>).map((providerKey) => {
            const provider = settings[providerKey];
            return (
              <div className="surface" key={providerKey}>
                <header className="surface-header">
                  <div>
                    <h4>{providerKey.toUpperCase()}</h4>
                    <p>为 {providerKey} 适配器预留 vendor、endpoint、model 和密钥提示位。</p>
                  </div>
                  <span className="meta-pill">{provider.enabled ? "Enabled" : "Disabled"}</span>
                </header>

                <div className="kv-grid">
                  <div className="field">
                    <label htmlFor={`${providerKey}-vendor`}>Vendor</label>
                    <input
                      id={`${providerKey}-vendor`}
                      onChange={(event) =>
                        updateProvider(providerKey, "vendor", event.target.value)
                      }
                      placeholder="deepgram / azure / openai / local"
                      value={provider.vendor}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor={`${providerKey}-model`}>Model</label>
                    <input
                      id={`${providerKey}-model`}
                      onChange={(event) =>
                        updateProvider(providerKey, "model", event.target.value)
                      }
                      placeholder="model identifier"
                      value={provider.model}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor={`${providerKey}-endpoint`}>Endpoint</label>
                    <input
                      id={`${providerKey}-endpoint`}
                      onChange={(event) =>
                        updateProvider(providerKey, "endpoint", event.target.value)
                      }
                      placeholder="https://..."
                      value={provider.endpoint}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor={`${providerKey}-api-key`}>API Key Hint</label>
                    <input
                      id={`${providerKey}-api-key`}
                      onChange={(event) =>
                        updateProvider(providerKey, "apiKeyHint", event.target.value)
                      }
                      placeholder="已脱敏后的密钥提示"
                      value={provider.apiKeyHint}
                    />
                  </div>
                </div>

                <div className="button-row">
                  <button
                    className="button button-secondary"
                    onClick={() => updateProvider(providerKey, "enabled", !provider.enabled)}
                    type="button"
                  >
                    {provider.enabled ? "Disable" : "Enable"}
                  </button>
                </div>
              </div>
            );
          })}
        </div>

        {onSubmit ? (
          <div className="button-row" style={{ marginTop: "1.25rem" }}>
            <button className="button button-primary" type="submit">
              {submitLabel}
            </button>
          </div>
        ) : null}
      </form>
    </section>
  );
}

export default ProviderConfigPanel;
