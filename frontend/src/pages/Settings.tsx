import { useEffect, useState } from "react";
import ProviderConfigPanel from "../components/ProviderConfigPanel";
import { apiClient } from "../lib/api";
import type { ProviderSettings } from "../types/providers";

const initialSettings: ProviderSettings = {
  stt: {
    vendor: "deepgram",
    endpoint: "wss://stt.example.com/stream",
    model: "nova-2",
    apiKeyHint: "dgm_***",
    enabled: true,
  },
  openclaw: {
    vendor: "openclaw-cloud",
    endpoint: "https://openclaw.example.com/reply",
    model: "default-agent",
    apiKeyHint: "oc_***",
    enabled: true,
  },
  tts: {
    vendor: "azure",
    endpoint: "https://tts.example.com/synthesize",
    model: "zh-CN-XiaoxiaoNeural",
    apiKeyHint: "az_***",
    enabled: true,
  },
};

function Settings() {
  const [settings, setSettings] = useState<ProviderSettings>(initialSettings);
  const [message, setMessage] = useState<string>(
    "配置表单已就绪，正在兼容后端当前 Provider 契约。",
  );

  useEffect(() => {
    let disposed = false;

    const load = async () => {
      try {
        const nextSettings = await apiClient.getProviderSettings();
        if (!disposed) {
          setSettings(nextSettings);
          setMessage("已加载后端当前 Provider 配置。");
        }
      } catch (loadError) {
        if (!disposed) {
          setMessage(loadError instanceof Error ? loadError.message : "加载 Provider 配置失败");
        }
      }
    };

    void load();

    return () => {
      disposed = true;
    };
  }, []);

  const saveSettings = async (nextSettings: ProviderSettings) => {
    try {
      const saved = await apiClient.saveProviderSettings(nextSettings);
      setSettings(saved);
      setMessage("Provider 配置已发送到 `/api/settings/providers`。");
    } catch (saveError) {
      setMessage(saveError instanceof Error ? saveError.message : "保存 Provider 配置失败");
    }
  };

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <p className="eyebrow">Settings</p>
          <h2>Configure STT, OpenClaw, and TTS adapters.</h2>
          <p>Settings 页面直接读取当前后端配置，并沿用前端适配层做字段转换。</p>
        </div>
        <span className="meta-pill">POST /api/settings/providers</span>
      </header>

      <p className="inline-message">{message}</p>

      <ProviderConfigPanel
        onChange={setSettings}
        onSubmit={saveSettings}
        settings={settings}
        submitLabel="Persist provider settings"
      />
    </div>
  );
}

export default Settings;
