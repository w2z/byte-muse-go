import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const target = loadEnv(mode, process.cwd(), "VITE_API_PROXY_TARGET").VITE_API_PROXY_TARGET || "http://127.0.0.1:3750";

  return {
    plugins: [react()],
    server: {
      proxy: {
        "/api": { target, changeOrigin: true },
        "/health": { target, changeOrigin: true },
      },
    },
  };
});
