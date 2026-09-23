import "@arco-design/web-react/dist/css/arco.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { routes } from "./app/routes";
import "./app/styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: false },
  },
});

async function enableMocking() {
  if (!import.meta.env.DEV) return;
  if (import.meta.env.VITE_ENABLE_MOCK !== "true") {
    // 旧开发页面可能已经安装 Service Worker，关闭 Mock 时同步移除。
    const registrations = await navigator.serviceWorker?.getRegistrations();
    const mockWorkers = (registrations ?? []).filter((item) => item.active?.scriptURL.endsWith("/mockServiceWorker.js"));
    await Promise.all(mockWorkers.map((item) => item.unregister()));
    if (mockWorkers.length > 0 && navigator.serviceWorker?.controller?.scriptURL.endsWith("/mockServiceWorker.js")) {
      window.location.reload();
      return;
    }
    return;
  }
  const { worker } = await import("./mocks/browser");
  await worker.start({ onUnhandledRequest: "bypass" });
}

const root = document.getElementById("root");
if (!root) {
  throw new Error("缺少应用根节点");
}

void enableMocking().then(() => {
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={createBrowserRouter(routes)} />
      </QueryClientProvider>
    </StrictMode>,
  );
});
