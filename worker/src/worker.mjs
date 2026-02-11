// riftgate signaling worker — JS glue for Go/Wasm Durable Object.
//
// This file exports:
// 1. The Worker fetch handler (auth + routing to Durable Object)
// 2. The SignalingRoom Durable Object class (WebSocket Hibernation API)
//
// The DO class bridges WebSocket events to Go/Wasm callbacks:
//   JS → Go: goOnJoin(wsId, peerId, publicKey), goOnMessage(wsId, json), goOnLeave(wsId)
//   Go → JS: jsSend(wsId, json)

import "./wasm_exec.js";
import wasmModule from "./app.wasm";

// ---------- Worker entry point ----------

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // Health check.
    if (url.pathname === "/status") {
      return new Response(JSON.stringify({ status: "ok" }), {
        headers: { "content-type": "application/json" },
      });
    }

    // WebSocket connect endpoint.
    if (url.pathname === "/connect") {
      // Validate bearer token.
      const authToken = env.AUTH_TOKEN;
      if (authToken) {
        const authHeader = request.headers.get("Authorization");
        if (!authHeader || authHeader !== `Bearer ${authToken}`) {
          return new Response("Unauthorized", { status: 401 });
        }
      }

      // Route to the Durable Object. All peers join the same room for now.
      // In the future, this could be parameterized by network name.
      const roomName = url.searchParams.get("room") || "default";
      const id = env.SIGNALING_ROOM.idFromName(roomName);
      const stub = env.SIGNALING_ROOM.get(id);
      return stub.fetch(request);
    }

    return new Response("Not Found", { status: 404 });
  },
};

// ---------- Durable Object: SignalingRoom ----------

export class SignalingRoom {
  constructor(ctx, env) {
    this.ctx = ctx;
    this.env = env;
    this.nextWsId = 1;

    // Go/Wasm readiness tracking.
    this.goReady = false;
    this.goReadyPromise = null;
  }

  // Initialize Go/Wasm runtime (lazy, once per DO instance).
  async ensureGo() {
    if (this.goReady) return;
    if (this.goReadyPromise) {
      await this.goReadyPromise;
      return;
    }

    this.goReadyPromise = new Promise((resolve, reject) => {
      // Set up the Go → JS send function before instantiating Wasm.
      const self = this;
      globalThis.jsSend = (wsId, jsonStr) => {
        self._sendToWebSocket(wsId, jsonStr);
      };

      // Signal from Go that it has registered all callbacks.
      globalThis.goReady = () => {
        self.goReady = true;
        resolve();
      };

      // Instantiate TinyGo Wasm.
      try {
        const go = new Go();
        const instance = new WebAssembly.Instance(wasmModule, go.importObject);
        go.run(instance);
      } catch (err) {
        reject(err);
      }
    });

    await this.goReadyPromise;
  }

  // Find a WebSocket by its wsId stored in the attachment.
  _findWebSocket(wsId) {
    const sockets = this.ctx.getWebSockets();
    for (const ws of sockets) {
      const attachment = ws.deserializeAttachment();
      if (attachment && attachment.wsId === wsId) {
        return ws;
      }
    }
    return null;
  }

  // Send a JSON message to a specific WebSocket by wsId.
  _sendToWebSocket(wsId, jsonStr) {
    const ws = this._findWebSocket(wsId);
    if (ws) {
      try {
        ws.send(jsonStr);
      } catch {
        // WebSocket may be closing; ignore send errors.
      }
    }
  }

  // Handle WebSocket upgrade requests.
  async fetch(request) {
    const upgradeHeader = request.headers.get("Upgrade");
    if (!upgradeHeader || upgradeHeader.toLowerCase() !== "websocket") {
      return new Response("Expected WebSocket upgrade", { status: 426 });
    }

    await this.ensureGo();

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);

    // Assign a unique wsId to this WebSocket.
    const wsId = this.nextWsId++;
    this.ctx.acceptWebSocket(server);
    server.serializeAttachment({ wsId, joined: false });

    return new Response(null, { status: 101, webSocket: client });
  }

  // Called when a WebSocket sends a message.
  async webSocketMessage(ws, message) {
    await this.ensureGo();

    const attachment = ws.deserializeAttachment();
    if (!attachment) return;

    const wsId = attachment.wsId;

    if (!attachment.joined) {
      // First message must be a join.
      let msg;
      try {
        msg = JSON.parse(message);
      } catch {
        return;
      }

      if (msg.type !== "join" || !msg.peerId) {
        return;
      }

      // Update attachment with peer info.
      ws.serializeAttachment({
        wsId,
        joined: true,
        peerId: msg.peerId,
        publicKey: msg.publicKey || "",
      });

      // Notify Go hub.
      globalThis.goOnJoin(wsId, msg.peerId, msg.publicKey || "");
      return;
    }

    // Forward subsequent messages to Go hub for routing.
    globalThis.goOnMessage(wsId, typeof message === "string" ? message : new TextDecoder().decode(message));
  }

  // Called when a WebSocket is closed.
  async webSocketClose(ws, code, reason, wasClean) {
    await this.ensureGo();

    const attachment = ws.deserializeAttachment();
    if (attachment && attachment.joined) {
      globalThis.goOnLeave(attachment.wsId);
    }

    // Complete the close handshake.
    try {
      ws.close(code, reason);
    } catch {
      // Already closed.
    }
  }

  // Called on WebSocket error.
  async webSocketError(ws, error) {
    await this.ensureGo();

    const attachment = ws.deserializeAttachment();
    if (attachment && attachment.joined) {
      globalThis.goOnLeave(attachment.wsId);
    }

    try {
      ws.close(1011, "WebSocket error");
    } catch {
      // Already closed.
    }
  }
}
