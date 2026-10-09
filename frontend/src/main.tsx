import "misans/lib/Normal/MiSansVF.min.css";
import "./app/theme.less";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { routes } from "./app/routes";
import "./app/styles.css";
import { API_BASE } from "./shared/api/client";
import { useSession } from "./shared/auth/session";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: false },
  },
});

const root = document.getElementById("root");
if (!root) {
  throw new Error("缺少应用根节点");
}

/**
 * 刷新页面时先恢复 HttpOnly 会话，再挂载受保护布局。
 * 如果直接先渲染管理布局，子页面的首个请求会在 401 后触发跳转，用户会看到一次明显的页面闪动。
 */
async function restoreSession(): Promise<void> {
  try {
    const response = await fetch(`${API_BASE}/auth/refresh`, {
      method: "POST",
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (response.ok) {
      const result = (await response.json()) as { user?: { id: string; username: string } };
      if (result.user) useSession.getState().setUser(result.user);
    }
  } catch {
    // 网络不可用时交给页面请求状态展示错误，不阻塞登录页挂载。
  }
}

void restoreSession().finally(() => {
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={createBrowserRouter(routes)} />
      </QueryClientProvider>
    </StrictMode>,
  );
});
