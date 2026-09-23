/* eslint-disable */
/* tslint:disable */
/**
 * Mock Service Worker.
 * @see https://github.com/mswjs/msw
 * - Please do NOT modify this file.
 */
const PACKAGE_VERSION = "2.15.0";
const INTEGRITY_CHECKSUM = "4db4a41e972cec1b64cc569c66952d82";
const IS_MOCKED_RESPONSE = Symbol("isMockedResponse");
const activeClientIds = new Set();

self.addEventListener("install", function () {
  self.skipWaiting();
});

self.addEventListener("activate", function (event) {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("message", async function (event) {
  const clientId = event.source.id;
  if (!clientId || !self.clients) return;
  const client = await self.clients.get(clientId);
  if (!client) return;
  const allClients = await self.clients.matchAll({ type: "window" });
  switch (event.data) {
    case "KEEPALIVE_REQUEST": {
      sendToClient(client, { type: "KEEPALIVE_RESPONSE" });
      break;
    }
    case "INTEGRITY_CHECK_REQUEST": {
      sendToClient(client, { type: "INTEGRITY_CHECK_RESPONSE", payload: { packageVersion: PACKAGE_VERSION, checksum: INTEGRITY_CHECKSUM } });
      break;
    }
    case "MOCK_ACTIVATE": {
      activeClientIds.add(clientId);
      sendToClient(client, { type: "MOCKING_ENABLED", payload: { client: { id: client.id, frameType: client.frameType } } });
      break;
    }
    case "CLIENT_CLOSED": {
      activeClientIds.delete(clientId);
      break;
    }
  }
});

self.addEventListener("fetch", function (event) {
  const requestInterceptedAt = new Date().getTime();
  if (event.request.mode === "navigate") return;
  if (activeClientIds.size === 0) return;
  const requestClone = event.request.clone();
  async function getResponse(client) {
    const clientMessage = await sendToClient(client, { type: "REQUEST", payload: { id: crypto.randomUUID(), url: requestClone.url, method: requestClone.method, headers: Object.fromEntries(requestClone.headers.entries()), body: await requestClone.text(), interceptedAt: requestInterceptedAt } }, [requestClone]);
    if (!clientMessage) return;
    switch (clientMessage.type) {
      case "MOCK_RESPONSE": {
        return respondWithMock(clientMessage.data);
      }
      case "PASSTHROUGH": {
        return;
      }
    }
  }
  event.respondWith((async function () {
    const client = await resolveMainClient(event);
    const response = await getResponse(client);
    if (response) return response;
    return fetch(event.request);
  })());
});

async function resolveMainClient(event) {
  const client = await self.clients.get(event.clientId);
  if (activeClientIds.has(event.clientId)) return client;
  return [...(await self.clients.matchAll({ type: "window" }))].find((windowClient) => activeClientIds.has(windowClient.id));
}

function sendToClient(client, message, transferrables = []) {
  return new Promise((resolve, reject) => {
    const channel = new MessageChannel();
    channel.port1.onmessage = (event) => {
      if (event.data && event.data.error) reject(event.data.error);
      else resolve(event.data);
    };
    client.postMessage(message, [channel.port2, ...transferrables.filter(Boolean)]);
  });
}

function respondWithMock(response) {
  return new Response(response.body, response);
}
