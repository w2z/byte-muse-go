import { http, HttpResponse } from "msw";

const now = "2026-09-23T00:00:00Z";

/** 开发 Mock 与 OpenAPI 0.1.0 字段一致，仅在 DEV 启用。 */
export const handlers = [
  http.post("/api/v1/auth/login", async () =>
    HttpResponse.json({ user: { id: "01J0USER000000000000000000", username: "admin" } }),
  ),
  http.post("/api/v1/auth/refresh", () =>
    HttpResponse.json({ user: { id: "01J0USER000000000000000000", username: "admin" } }),
  ),
  http.get("/api/v1/dashboard", () =>
    HttpResponse.json({
      active_subscriptions: 1,
      completed_downloads: 0,
      media_count: 2,
      healthy_integrations: 1,
    }),
  ),
  http.get("/api/v1/media", () =>
    HttpResponse.json({
      page: 1,
      page_size: 20,
      total: 1,
      items: [
        {
          id: "01J0MEDIA00000000000000000",
          code: "ABC-001",
          title: "演示影片",
          translated_title: null,
          subscription_status: "active",
          library_status: "absent",
          created_at: now,
          updated_at: now,
        },
      ],
    }),
  ),
  http.get("/api/v1/subscriptions", () =>
    HttpResponse.json({
      page: 1,
      page_size: 20,
      total: 1,
      items: [
        {
          id: "01J0SUBSCRIPTION000000000000",
          media_id: "01J0MEDIA00000000000000000",
          status: "active",
          mode: "strict",
          filter: {},
          created_at: now,
          updated_at: now,
          version: 1,
        },
      ],
    }),
  ),
  http.post("/api/v1/subscriptions/:subscriptionId/cancel", () =>
    HttpResponse.json({
      id: "01J0SUBSCRIPTION000000000000",
      media_id: "01J0MEDIA00000000000000000",
      status: "canceled",
      mode: "strict",
      filter: {},
      created_at: now,
      updated_at: now,
      version: 2,
    }),
  ),
  http.get("/api/v1/downloads", () =>
    HttpResponse.json({ page: 1, page_size: 20, total: 0, items: [] }),
  ),
  http.get("/api/v1/system/status", () =>
    HttpResponse.json({
      version: "0.1.0",
      database_driver: "sqlite",
      scheduler_running: true,
      started_at: now,
    }),
  ),
  http.get("/api/v1/system/settings", () =>
    HttpResponse.json({ database_driver: "sqlite", demo_seed_enabled: false }),
  ),
];
