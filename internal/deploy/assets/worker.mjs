// riftgate signaling worker — JS glue for Go/Wasm Durable Object.
//
// This file exports:
// 1. The Worker fetch handler (auth + routing to Durable Object)
// 2. The SignalingRoom Durable Object class (WebSocket Hibernation API)
//
// The DO class bridges WebSocket events to Go/Wasm callbacks:
//   JS → Go: goOnJoin(wsId, peerId, publicKey, address, routesJSON), goOnMessage(wsId, json), goOnLeave(wsId)
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

    // Invite creation (authenticated).
    if (url.pathname === "/invite" && request.method === "POST") {
      const authToken = env.AUTH_TOKEN;
      if (authToken) {
        const authHeader = request.headers.get("Authorization");
        if (!authHeader || authHeader !== `Bearer ${authToken}`) {
          return new Response("Unauthorized", { status: 401 });
        }
      }

      const roomName = "default";
      const id = env.SIGNALING_ROOM.idFromName(roomName);
      const stub = env.SIGNALING_ROOM.get(id);

      // Forward to DO with a special header to distinguish from WebSocket upgrade.
      const doReq = new Request(request.url, {
        method: "POST",
        headers: { "X-Riftgate-Action": "create-invite" },
        body: request.body,
      });
      return stub.fetch(doReq);
    }

    // Invite redemption (unauthenticated — the code itself is the credential).
    const inviteMatch = url.pathname.match(/^\/invite\/([A-Za-z0-9-]+)$/);
    if (inviteMatch && request.method === "GET") {
      const roomName = "default";
      const id = env.SIGNALING_ROOM.idFromName(roomName);
      const stub = env.SIGNALING_ROOM.get(id);

      const doReq = new Request(request.url, {
        method: "GET",
        headers: { "X-Riftgate-Action": "redeem-invite", "X-Riftgate-Invite-Code": inviteMatch[1] },
      });
      return stub.fetch(doReq);
    }

    // Network info (authenticated) — returns subnet and assigned addresses.
    if (url.pathname === "/network-info" && request.method === "GET") {
      const authToken = env.AUTH_TOKEN;
      if (authToken) {
        const authHeader = request.headers.get("Authorization");
        if (!authHeader || authHeader !== `Bearer ${authToken}`) {
          return new Response("Unauthorized", { status: 401 });
        }
      }

      const roomName = "default";
      const id = env.SIGNALING_ROOM.idFromName(roomName);
      const stub = env.SIGNALING_ROOM.get(id);

      const doReq = new Request(request.url, {
        method: "GET",
        headers: { "X-Riftgate-Action": "network-info" },
      });
      return stub.fetch(doReq);
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

  // Ensure SQLite tables exist for invite and address tracking.
  _ensureTables() {
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS invites (
        code TEXT PRIMARY KEY,
        auth_token TEXT NOT NULL,
        server_url TEXT NOT NULL,
        subnet TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        redeemed INTEGER NOT NULL DEFAULT 0
      )
    `);
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS devices (
        address TEXT PRIMARY KEY,
        device_name TEXT,
        public_key TEXT,
        created_at INTEGER NOT NULL
      )
    `);
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS network (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      )
    `);
  }

  // Get or initialize the network subnet.
  _getSubnet() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT value FROM network WHERE key = 'subnet'")];
    if (rows.length > 0) {
      return rows[0].value;
    }
    // Default subnet.
    const subnet = "10.0.0.0/24";
    this.ctx.storage.sql.exec("INSERT INTO network (key, value) VALUES ('subnet', ?)", subnet);
    return subnet;
  }

  // Get all assigned addresses.
  _getAssignedAddresses() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT address, device_name, public_key FROM devices ORDER BY address")];
    return rows.map(r => ({ address: r.address, device_name: r.device_name, public_key: r.public_key }));
  }

  // Assign the next available address in the subnet.
  _assignNextAddress() {
    const subnet = this._getSubnet();
    const assigned = this._getAssignedAddresses();

    // Parse subnet (e.g. "10.0.0.0/24").
    const [baseIP, prefixStr] = subnet.split("/");
    const prefix = parseInt(prefixStr, 10);
    const baseParts = baseIP.split(".").map(Number);
    const baseNum = (baseParts[0] << 24) | (baseParts[1] << 16) | (baseParts[2] << 8) | baseParts[3];
    const hostBits = 32 - prefix;
    const maxHosts = (1 << hostBits) - 2; // Exclude network and broadcast.

    // Collect used host numbers.
    const usedHosts = new Set();
    for (const dev of assigned) {
      const addrIP = dev.address.split("/")[0];
      const parts = addrIP.split(".").map(Number);
      const num = (parts[0] << 24) | (parts[1] << 16) | (parts[2] << 8) | parts[3];
      const hostNum = num - baseNum;
      usedHosts.add(hostNum);
    }

    // Find next available (start at .1).
    for (let h = 1; h <= maxHosts; h++) {
      if (!usedHosts.has(h)) {
        const addr = baseNum + h;
        const ip = `${(addr >>> 24) & 255}.${(addr >>> 16) & 255}.${(addr >>> 8) & 255}.${addr & 255}`;
        return `${ip}/${prefix}`;
      }
    }

    return null; // Subnet exhausted.
  }

  // Register a device address as assigned.
  _registerDevice(address, deviceName, publicKey) {
    this._ensureTables();
    this.ctx.storage.sql.exec(
      "INSERT OR REPLACE INTO devices (address, device_name, public_key, created_at) VALUES (?, ?, ?, ?)",
      address, deviceName || "", publicKey || "", Math.floor(Date.now() / 1000)
    );
  }

  // Handle invite creation.
  async _handleCreateInvite(request) {
    this._ensureTables();

    const serverURL = new URL(request.url);
    const workerBaseURL = `${serverURL.protocol}//${serverURL.host}`;

    // Generate an 8-character invite code (AB12-XY34 format).
    const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // No 0/O/1/I to avoid confusion.
    let code = "";
    const bytes = new Uint8Array(8);
    crypto.getRandomValues(bytes);
    for (let i = 0; i < 8; i++) {
      code += chars[bytes[i] % chars.length];
    }
    code = code.slice(0, 4) + "-" + code.slice(4);

    const subnet = this._getSubnet();
    const now = Math.floor(Date.now() / 1000);
    const expiresAt = now + 600; // 10 minutes.

    this.ctx.storage.sql.exec(
      "INSERT INTO invites (code, auth_token, server_url, subnet, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
      code, this.env.AUTH_TOKEN || "", workerBaseURL, subnet, now, expiresAt
    );

    return new Response(JSON.stringify({
      code,
      expires_in: 600,
    }), {
      headers: { "content-type": "application/json" },
    });
  }

  // Handle invite redemption.
  async _handleRedeemInvite(code) {
    this._ensureTables();

    // Normalize code — strip dashes for lookup.
    const normalizedInput = code.replace(/-/g, "").toUpperCase();

    // Look up by normalized code.
    const rows = [...this.ctx.storage.sql.exec("SELECT * FROM invites WHERE REPLACE(code, '-', '') = ? AND redeemed = 0", normalizedInput)];
    if (rows.length === 0) {
      return new Response(JSON.stringify({ error: "invalid or expired invite code" }), {
        status: 404,
        headers: { "content-type": "application/json" },
      });
    }

    const invite = rows[0];
    const now = Math.floor(Date.now() / 1000);

    if (now > invite.expires_at) {
      return new Response(JSON.stringify({ error: "invite code has expired" }), {
        status: 410,
        headers: { "content-type": "application/json" },
      });
    }

    // Assign next address.
    const nextAddress = this._assignNextAddress();
    if (!nextAddress) {
      return new Response(JSON.stringify({ error: "no addresses available in subnet" }), {
        status: 507,
        headers: { "content-type": "application/json" },
      });
    }

    // Mark as redeemed.
    this.ctx.storage.sql.exec("UPDATE invites SET redeemed = 1 WHERE code = ?", invite.code);

    // Register the address (device name/key will be updated on first connect).
    this._registerDevice(nextAddress, "", "");

    return new Response(JSON.stringify({
      server_url: invite.server_url,
      auth_token: invite.auth_token,
      address: nextAddress,
      subnet: invite.subnet,
    }), {
      headers: { "content-type": "application/json" },
    });
  }

  // Handle network info request.
  async _handleNetworkInfo() {
    const subnet = this._getSubnet();
    const devices = this._getAssignedAddresses();
    const nextAddress = this._assignNextAddress();

    return new Response(JSON.stringify({
      subnet,
      devices,
      next_address: nextAddress,
    }), {
      headers: { "content-type": "application/json" },
    });
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

    // Rehydrate Go peer state from surviving WebSocket attachments.
    // After hibernation, the Wasm module is re-instantiated with empty state,
    // but WebSocket connections and their attachments survive. Re-register
    // all previously joined peers so the Go hub knows about them.
    this._rehydrate();
  }

  // Rebuild Go hub state from WebSocket attachments after hibernation wake.
  _rehydrate() {
    const sockets = this.ctx.getWebSockets();
    let maxWsId = this.nextWsId;
    for (const ws of sockets) {
      const attachment = ws.deserializeAttachment();
      if (!attachment || !attachment.joined) continue;
      if (attachment.wsId >= maxWsId) {
        maxWsId = attachment.wsId + 1;
      }
      globalThis.goOnRehydrate(attachment.wsId, attachment.peerId, attachment.publicKey || "", attachment.address || "", JSON.stringify(attachment.routes || []));
    }
    this.nextWsId = maxWsId;
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

  // Handle all HTTP requests to the Durable Object.
  async fetch(request) {
    const action = request.headers.get("X-Riftgate-Action");

    // Handle non-WebSocket management requests.
    if (action === "create-invite") {
      return this._handleCreateInvite(request);
    }
    if (action === "redeem-invite") {
      const code = request.headers.get("X-Riftgate-Invite-Code");
      return this._handleRedeemInvite(code);
    }
    if (action === "network-info") {
      return this._handleNetworkInfo();
    }

    // WebSocket upgrade.
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
        address: msg.address || "",
        routes: msg.routes || [],
      });

      // Register device address in SQLite for address tracking.
      if (msg.address) {
        this._registerDevice(msg.address, msg.peerId, msg.publicKey || "");
      }

      // Notify Go hub.
      globalThis.goOnJoin(wsId, msg.peerId, msg.publicKey || "", msg.address || "", JSON.stringify(msg.routes || []));
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
