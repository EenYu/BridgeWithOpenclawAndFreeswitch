import react from "@vitejs/plugin-react-swc";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = resolveApiTarget(env.VITE_API_BASE_URL);
  const wsTarget = resolveWsTarget(env.VITE_WS_URL, apiTarget);

  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy:
        apiTarget || wsTarget
          ? {
              "/api": {
                target: apiTarget,
                changeOrigin: true,
                secure: false,
              },
              "/ws": {
                target: wsTarget,
                changeOrigin: true,
                secure: false,
                ws: true,
              },
            }
          : undefined,
    },
  };
});

function resolveApiTarget(value?: string): string | undefined {
  if (!value) {
    return undefined;
  }

  try {
    return new URL(value).origin;
  } catch {
    return undefined;
  }
}

function resolveWsTarget(value: string | undefined, apiTarget: string | undefined): string | undefined {
  if (value) {
    try {
      const parsed = new URL(value);
      const protocol = parsed.protocol === "wss:" ? "https:" : "http:";
      return `${protocol}//${parsed.host}`;
    } catch {
      return apiTarget;
    }
  }

  return apiTarget;
}
