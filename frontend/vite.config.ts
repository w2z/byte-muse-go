import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const target = loadEnv(mode, process.cwd(), "VITE_API_PROXY_TARGET").VITE_API_PROXY_TARGET || "http://127.0.0.1:3750";
  // 发布构建与后端共用 BYTEMUSE_VERSION；本地开发从版本记录读取，注入静态资源以免首屏等待接口。
  const version = process.env.BYTEMUSE_VERSION?.trim()
    || (JSON.parse(readFileSync(new URL("../version.json", import.meta.url), "utf8")) as { version: string }).version;

  return {
    plugins: [react()],
    define: { "import.meta.env.VITE_APP_VERSION": JSON.stringify(version) },
    server: {
      proxy: {
        "/api": { target, changeOrigin: true },
        "/health": { target, changeOrigin: true },
      },
    },
  };
});
