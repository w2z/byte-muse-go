// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { UploadPathsField } from "./UploadPathsField";
import { apiRequest } from "../../shared/api/client";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn() }));
afterEach(cleanup);

it("目录正在上传时禁用该行删除按钮", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.mocked(apiRequest).mockImplementation(async (path) => {
    if (String(path).startsWith("/cloud-upload/directories/progress")) {
      return { items: [{ kind: "115", local_path: "/videos", remote_path: "/cloud" }] };
    }
    return { actions: { scan: true } };
  });
  const value = [
    { kind: "115" as const, id: "1", path: "/cloud", local_path: "/videos" },
    { kind: "cd2" as const, id: "/idle", path: "/idle", local_path: "/idle" },
  ];
  render(
    <QueryClientProvider client={client}>
      <UploadPathsField value={value} savedValue={value} onChange={() => undefined} />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByRole("button", { name: "删除目录映射 1" })).toBeDisabled());
  expect(screen.getByRole("button", { name: "删除目录映射 2" })).toBeEnabled();
  client.clear();
});
