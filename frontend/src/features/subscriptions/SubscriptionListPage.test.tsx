import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { SubscriptionListPage } from "./SubscriptionListPage";

const subscription = {
  id: "01J0SUBSCRIPTION000000000000",
  media_id: "01J0MEDIA00000000000000000",
  status: "active",
  mode: "strict",
  filter: {},
  created_at: "2026-09-23T00:00:00Z",
  updated_at: "2026-09-23T00:00:00Z",
  version: 1,
};

describe("订阅操作防重复提交", () => {
  it("取消请求尚未返回时不能再次提交", async () => {
    const user = userEvent.setup();
    let resolveRequest: (value: Response) => void = () => undefined;
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.startsWith("/api/v1/subscriptions?") && method === "GET") {
        return Promise.resolve(
          new Response(
            JSON.stringify({ page: 1, page_size: 20, total: 1, items: [subscription] }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return new Promise<Response>((resolve) => {
        resolveRequest = resolve;
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <SubscriptionListPage />
      </QueryClientProvider>,
    );

    const button = await screen.findByRole("button", { name: "取消订阅" });
    await user.click(button);
    await user.click(button);

    const cancelCalls = fetchMock.mock.calls.filter((call) => {
      const init = call[1] as RequestInit | undefined;
      return init?.method === "POST" && String(call[0]).endsWith("/cancel");
    });
    expect(cancelCalls).toHaveLength(1);
    expect(String(cancelCalls[0]?.[0])).toBe(
      `/api/v1/subscriptions/${subscription.id}/cancel`,
    );
    expect(button).toBeDisabled();

    resolveRequest(
      new Response(JSON.stringify({ ...subscription, status: "canceled", version: 2 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  });
});
