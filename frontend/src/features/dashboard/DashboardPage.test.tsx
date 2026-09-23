import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DashboardPage } from "./DashboardPage";

describe("仪表盘数据边界", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("事件流无法连接时展示诚实的空态", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ active_subscriptions: 0, completed_downloads: 0, media_count: 2, healthy_integrations: 0 }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <DashboardPage />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("暂无新事件")).toBeInTheDocument();
    expect(screen.queryByText("100%")).not.toBeInTheDocument();
    expect(screen.queryByText("所有已连接服务响应正常")).not.toBeInTheDocument();
  });

  it("事件流发送 ready 后展示已连接状态", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ active_subscriptions: 0, completed_downloads: 0, media_count: 0, healthy_integrations: 0 }), { status: 200, headers: { "Content-Type": "application/json" } })));
    class ReadyEventSource {
      addEventListener(name: string, listener: EventListener) {
        if (name === "ready") queueMicrotask(() => listener(new Event("ready")));
      }
      removeEventListener() {}
      close() {}
    }
    vi.stubGlobal("EventSource", ReadyEventSource);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><DashboardPage /></QueryClientProvider>);
    expect(await screen.findByText("事件流已连接，暂无新事件")).toBeInTheDocument();
  });
});
