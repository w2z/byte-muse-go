// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { apiRequest } from "../../shared/api/client";
import { AllFilmsPage } from "./AllFilmsPage";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 15 })) }));

it("影片类型交给服务端筛选并可恢复全部类型", async () => {
 const user = userEvent.setup();
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<QueryClientProvider client={client}><AllFilmsPage /></QueryClientProvider>);
 await user.click(screen.getByLabelText("影片类型筛选"));
 await waitFor(async () => { await user.click(screen.getByText("无码破解")); });
 await waitFor(() => expect(apiRequest).toHaveBeenLastCalledWith("/media?page=1&page_size=15&video_type=uncensored_cracked"));
 await user.click(screen.getByLabelText("影片类型筛选"));
 await waitFor(async () => { await user.click(screen.getByText("全部类型")); });
 await waitFor(() => expect(apiRequest).toHaveBeenLastCalledWith("/media?page=1&page_size=15"));
});
