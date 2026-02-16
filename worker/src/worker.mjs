// bamgate signaling worker — JS glue for Go/Wasm Durable Object.
//
// This file exports:
// 1. The Worker fetch handler (routing to Durable Object)
// 2. The SignalingRoom Durable Object class (WebSocket Hibernation API)
//
// Authentication uses GitHub OAuth + Worker-minted JWTs:
//   - POST /auth/register — exchange GitHub token for device credentials
//   - POST /auth/refresh  — rotate refresh token and get new JWT
//   - All other endpoints require a valid JWT in Authorization header
//
// The DO class bridges WebSocket events to Go/Wasm callbacks:
//   Signaling:
//     JS -> Go: goOnJoin, goOnMessage, goOnLeave, goOnRehydrate
//     Go -> JS: jsSend
//   TURN relay:
//     JS -> Go: goOnTURNMessage, goOnTURNClose
//     Go -> JS: jsSendBinary, jsTURNSecret

import "./wasm_exec.js";
import wasmModule from "./app.wasm";

// ---------- Helpers: base64url encoding ----------

function base64urlEncode(data) {
  const bytes = data instanceof Uint8Array ? data : new TextEncoder().encode(data);
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64urlDecode(str) {
  str = str.replace(/-/g, "+").replace(/_/g, "/");
  while (str.length % 4 !== 0) str += "=";
  const binary = atob(str);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function hexEncode(bytes) {
  return Array.from(bytes).map(b => b.toString(16).padStart(2, "0")).join("");
}

function hexDecode(hex) {
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substring(i, i + 2), 16);
  }
  return bytes;
}

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

    // Route to the "default" room DO for all endpoints.
    const roomName = url.searchParams.get("room") || "default";
    const id = env.SIGNALING_ROOM.idFromName(roomName);
    const stub = env.SIGNALING_ROOM.get(id);

    // --- Unauthenticated auth endpoints (credentials in body) ---

    if (url.pathname === "/auth/register" && request.method === "POST") {
      const doReq = new Request(request.url, {
        method: "POST",
        headers: { "X-Bamgate-Action": "auth-register", "Content-Type": "application/json" },
        body: request.body,
      });
      return stub.fetch(doReq);
    }

    if (url.pathname === "/auth/refresh" && request.method === "POST") {
      const doReq = new Request(request.url, {
        method: "POST",
        headers: { "X-Bamgate-Action": "auth-refresh", "Content-Type": "application/json" },
        body: request.body,
      });
      return stub.fetch(doReq);
    }

    // --- JWT-authenticated endpoints ---

    // Extract bearer token from Authorization header.
    const authHeader = request.headers.get("Authorization");
    if (!authHeader || !authHeader.startsWith("Bearer ")) {
      return new Response(JSON.stringify({ error: "missing authorization" }), {
        status: 401,
        headers: { "content-type": "application/json" },
      });
    }
    const token = authHeader.slice(7);

    // Forward token to DO for validation via header.
    const authHeaders = [["X-Bamgate-JWT", token]];

    // WebSocket connect endpoint (signaling).
    if (url.pathname === "/connect") {
      const headers = new Headers([...request.headers.entries(), ...authHeaders]);
      const doReq = new Request(request.url, { method: request.method, headers });
      return stub.fetch(doReq);
    }

    // TURN relay WebSocket endpoint.
    if (url.pathname === "/turn") {
      const headers = new Headers([...request.headers.entries(), ...authHeaders, ["X-Bamgate-Turn", "1"]]);
      const doReq = new Request(request.url, { method: request.method, headers });
      return stub.fetch(doReq);
    }

    // List devices.
    if (url.pathname === "/auth/devices" && request.method === "GET") {
      const doReq = new Request(request.url, {
        method: "GET",
        headers: { "X-Bamgate-Action": "list-devices", "X-Bamgate-JWT": token },
      });
      return stub.fetch(doReq);
    }

    // Revoke device.
    const revokeMatch = url.pathname.match(/^\/auth\/devices\/([a-f0-9-]+)$/);
    if (revokeMatch && request.method === "DELETE") {
      const doReq = new Request(request.url, {
        method: "DELETE",
        headers: {
          "X-Bamgate-Action": "revoke-device",
          "X-Bamgate-JWT": token,
          "X-Bamgate-Device-ID": revokeMatch[1],
        },
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

    // Cache for imported HMAC keys (kid -> CryptoKey).
    this._keyCache = new Map();
  }

  // ==================== SQLite Schema ====================

  _ensureTables() {
    if (this._tablesReady) return;

    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS signing_keys (
        kid TEXT PRIMARY KEY,
        secret_hex TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        revoked_at INTEGER
      )
    `);
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS owners (
        github_id TEXT PRIMARY KEY,
        username TEXT NOT NULL,
        created_at INTEGER NOT NULL
      )
    `);
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS devices (
        device_id TEXT PRIMARY KEY,
        device_name TEXT NOT NULL,
        owner_github_id TEXT NOT NULL,
        address TEXT NOT NULL,
        refresh_token_hash TEXT NOT NULL,
        refresh_token_expires_at INTEGER NOT NULL,
        revoked INTEGER DEFAULT 0,
        created_at INTEGER NOT NULL,
        last_seen_at INTEGER
      )
    `);
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS network (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      )
    `);

    this._tablesReady = true;
  }

  // ==================== Network Config ====================

  _getSubnet() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT value FROM network WHERE key = 'subnet'")];
    if (rows.length > 0) return rows[0].value;
    const subnet = "10.0.0.0/24";
    this.ctx.storage.sql.exec("INSERT INTO network (key, value) VALUES ('subnet', ?)", subnet);
    return subnet;
  }

  _getOrCreateTURNSecret() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT value FROM network WHERE key = 'turn_secret'")];
    if (rows.length > 0) return rows[0].value;
    // Generate a new TURN secret.
    const bytes = new Uint8Array(24);
    crypto.getRandomValues(bytes);
    const secret = "bg_" + hexEncode(bytes);
    this.ctx.storage.sql.exec("INSERT INTO network (key, value) VALUES ('turn_secret', ?)", secret);
    return secret;
  }

  // ==================== Address Assignment ====================

  _getAssignedAddresses() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT address FROM devices WHERE revoked = 0 ORDER BY address")];
    return rows.map(r => r.address);
  }

  _assignNextAddress() {
    const subnet = this._getSubnet();
    const assigned = this._getAssignedAddresses();

    const [baseIP, prefixStr] = subnet.split("/");
    const prefix = parseInt(prefixStr, 10);
    const baseParts = baseIP.split(".").map(Number);
    const baseNum = (baseParts[0] << 24) | (baseParts[1] << 16) | (baseParts[2] << 8) | baseParts[3];
    const hostBits = 32 - prefix;
    const maxHosts = (1 << hostBits) - 2;

    const usedHosts = new Set();
    for (const addr of assigned) {
      const addrIP = addr.split("/")[0];
      const parts = addrIP.split(".").map(Number);
      const num = (parts[0] << 24) | (parts[1] << 16) | (parts[2] << 8) | parts[3];
      usedHosts.add(num - baseNum);
    }

    for (let h = 1; h <= maxHosts; h++) {
      if (!usedHosts.has(h)) {
        const addr = baseNum + h;
        const ip = `${(addr >>> 24) & 255}.${(addr >>> 16) & 255}.${(addr >>> 8) & 255}.${addr & 255}`;
        return `${ip}/${prefix}`;
      }
    }
    return null;
  }

  // ==================== JWT Signing Keys ====================

  async _getOrCreateSigningKey() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec(
      "SELECT kid, secret_hex FROM signing_keys WHERE revoked_at IS NULL ORDER BY created_at DESC LIMIT 1"
    )];
    if (rows.length > 0) {
      return { kid: rows[0].kid, secretHex: rows[0].secret_hex };
    }

    // Generate a new 256-bit HMAC key.
    const keyBytes = new Uint8Array(32);
    crypto.getRandomValues(keyBytes);
    const secretHex = hexEncode(keyBytes);

    // Generate kid.
    const kidBytes = new Uint8Array(8);
    crypto.getRandomValues(kidBytes);
    const kid = "k_" + hexEncode(kidBytes);

    const now = Math.floor(Date.now() / 1000);
    this.ctx.storage.sql.exec(
      "INSERT INTO signing_keys (kid, secret_hex, created_at) VALUES (?, ?, ?)",
      kid, secretHex, now
    );

    return { kid, secretHex };
  }

  async _importHMACKey(secretHex) {
    if (this._keyCache.has(secretHex)) {
      return this._keyCache.get(secretHex);
    }
    const keyData = hexDecode(secretHex);
    const key = await crypto.subtle.importKey(
      "raw", keyData, { name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]
    );
    this._keyCache.set(secretHex, key);
    return key;
  }

  // ==================== JWT Operations ====================

  async _signJWT(payload) {
    const { kid, secretHex } = await this._getOrCreateSigningKey();
    const key = await this._importHMACKey(secretHex);

    const header = { alg: "HS256", typ: "JWT", kid };
    const encodedHeader = base64urlEncode(JSON.stringify(header));
    const encodedPayload = base64urlEncode(JSON.stringify(payload));
    const signingInput = encodedHeader + "." + encodedPayload;

    const signature = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(signingInput));
    const encodedSignature = base64urlEncode(new Uint8Array(signature));

    return signingInput + "." + encodedSignature;
  }

  async _verifyJWT(token) {
    const parts = token.split(".");
    if (parts.length !== 3) return null;

    let header;
    try {
      header = JSON.parse(new TextDecoder().decode(base64urlDecode(parts[0])));
    } catch {
      return null;
    }

    if (header.alg !== "HS256" || !header.kid) return null;

    // Look up the signing key by kid.
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec(
      "SELECT secret_hex FROM signing_keys WHERE kid = ? AND revoked_at IS NULL", header.kid
    )];
    if (rows.length === 0) return null;

    const key = await this._importHMACKey(rows[0].secret_hex);

    // Verify signature.
    const signingInput = new TextEncoder().encode(parts[0] + "." + parts[1]);
    const signature = base64urlDecode(parts[2]);
    const valid = await crypto.subtle.verify("HMAC", key, signature, signingInput);
    if (!valid) return null;

    // Decode and check expiry.
    let payload;
    try {
      payload = JSON.parse(new TextDecoder().decode(base64urlDecode(parts[1])));
    } catch {
      return null;
    }

    const now = Math.floor(Date.now() / 1000);
    if (payload.exp && payload.exp < now) return null;

    return payload;
  }

  // ==================== Refresh Token Helpers ====================

  _generateRefreshToken() {
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    return "bgr_" + hexEncode(bytes);
  }

  async _hashToken(token) {
    const data = new TextEncoder().encode(token);
    const hash = await crypto.subtle.digest("SHA-256", data);
    return hexEncode(new Uint8Array(hash));
  }

  // ==================== Auth: Register ====================

  async _handleAuthRegister(request) {
    let body;
    try {
      body = await request.json();
    } catch {
      return this._jsonError("invalid request body", 400);
    }

    const { device_name, github_token } = body;
    if (!device_name) return this._jsonError("device_name is required", 400);
    if (!github_token) return this._jsonError("github_token is required", 400);

    // Verify GitHub token by calling GitHub API.
    const ghResp = await fetch("https://api.github.com/user", {
      headers: {
        "Authorization": `Bearer ${github_token}`,
        "User-Agent": "bamgate-worker",
        "Accept": "application/vnd.github+json",
      },
    });

    if (!ghResp.ok) {
      return this._jsonError("invalid GitHub token", 401);
    }

    const ghUser = await ghResp.json();
    const githubId = String(ghUser.id);
    const username = ghUser.login;

    // Check owner table.
    this._ensureTables();
    const owners = [...this.ctx.storage.sql.exec("SELECT github_id FROM owners")];

    if (owners.length === 0) {
      // First registration — this user becomes the owner.
      const now = Math.floor(Date.now() / 1000);
      this.ctx.storage.sql.exec(
        "INSERT INTO owners (github_id, username, created_at) VALUES (?, ?, ?)",
        githubId, username, now
      );
    } else if (owners[0].github_id !== githubId) {
      return this._jsonError("unauthorized: you are not the owner of this network", 403);
    }

    // Check if a non-revoked device with the same name already exists for this owner.
    const existing = [...this.ctx.storage.sql.exec(
      "SELECT device_id, address FROM devices WHERE device_name = ? AND owner_github_id = ? AND revoked = 0",
      device_name, githubId
    )];

    const now = Math.floor(Date.now() / 1000);
    const refreshToken = this._generateRefreshToken();
    const refreshTokenHash = await this._hashToken(refreshToken);
    const refreshExpiresAt = now + 30 * 24 * 60 * 60; // 30 days.

    let deviceId, address;

    if (existing.length > 0) {
      // Reclaim existing device — reuse its ID and address, reset credentials.
      deviceId = existing[0].device_id;
      address = existing[0].address;
      this.ctx.storage.sql.exec(
        `UPDATE devices SET refresh_token_hash = ?, refresh_token_expires_at = ?, last_seen_at = ?
         WHERE device_id = ?`,
        refreshTokenHash, refreshExpiresAt, now, deviceId
      );
    } else {
      // New device — assign a fresh address and ID.
      address = this._assignNextAddress();
      if (!address) {
        return this._jsonError("no addresses available in subnet", 507);
      }
      deviceId = crypto.randomUUID();
      this.ctx.storage.sql.exec(
        `INSERT INTO devices (device_id, device_name, owner_github_id, address, refresh_token_hash,
          refresh_token_expires_at, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        deviceId, device_name, githubId, address, refreshTokenHash, refreshExpiresAt, now, now
      );
    }

    // Get or create TURN secret.
    const turnSecret = this._getOrCreateTURNSecret();

    // Get subnet.
    const subnet = this._getSubnet();

    // Sign access JWT.
    const accessToken = await this._signJWT({
      sub: deviceId,
      owner: githubId,
      net: "default",
      iat: now,
      exp: now + 3600, // 1 hour.
    });

    // Build server URL from request.
    const reqURL = new URL(request.url);
    const serverURL = `${reqURL.protocol}//${reqURL.host}`;

    return this._jsonResponse({
      device_id: deviceId,
      access_token: accessToken,
      refresh_token: refreshToken,
      address,
      subnet,
      turn_secret: turnSecret,
      server_url: serverURL,
    });
  }

  // ==================== Auth: Refresh ====================

  async _handleAuthRefresh(request) {
    let body;
    try {
      body = await request.json();
    } catch {
      return this._jsonError("invalid request body", 400);
    }

    const { device_id, refresh_token } = body;
    if (!device_id) return this._jsonError("device_id is required", 400);
    if (!refresh_token) return this._jsonError("refresh_token is required", 400);

    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec(
      "SELECT * FROM devices WHERE device_id = ? AND revoked = 0", device_id
    )];
    if (rows.length === 0) {
      return this._jsonError("device not found or revoked", 401);
    }

    const device = rows[0];
    const now = Math.floor(Date.now() / 1000);

    // Check refresh token expiry.
    if (now > device.refresh_token_expires_at) {
      return this._jsonError("refresh_token_expired", 401);
    }

    // Verify refresh token hash.
    const providedHash = await this._hashToken(refresh_token);
    if (providedHash !== device.refresh_token_hash) {
      return this._jsonError("invalid refresh token", 401);
    }

    // Rotate: generate new refresh token.
    const newRefreshToken = this._generateRefreshToken();
    const newHash = await this._hashToken(newRefreshToken);
    const newExpiresAt = now + 30 * 24 * 60 * 60; // 30 days rolling.

    this.ctx.storage.sql.exec(
      `UPDATE devices SET refresh_token_hash = ?, refresh_token_expires_at = ?, last_seen_at = ?
       WHERE device_id = ?`,
      newHash, newExpiresAt, now, device_id
    );

    // Sign new access JWT.
    const accessToken = await this._signJWT({
      sub: device_id,
      owner: device.owner_github_id,
      net: "default",
      iat: now,
      exp: now + 3600,
    });

    return this._jsonResponse({
      access_token: accessToken,
      refresh_token: newRefreshToken,
      expires_in: 3600,
    });
  }

  // ==================== Auth: List Devices ====================

  async _handleListDevices(claims) {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec(
      `SELECT device_id, device_name, address, created_at, last_seen_at, revoked
       FROM devices WHERE owner_github_id = ? ORDER BY created_at`,
      claims.owner
    )];

    return this._jsonResponse({
      devices: rows.map(r => ({
        device_id: r.device_id,
        device_name: r.device_name,
        address: r.address,
        created_at: r.created_at,
        last_seen_at: r.last_seen_at,
        revoked: r.revoked === 1,
      })),
    });
  }

  // ==================== Auth: Revoke Device ====================

  async _handleRevokeDevice(claims, targetDeviceId) {
    if (claims.sub === targetDeviceId) {
      return this._jsonError("cannot revoke your own device", 400);
    }

    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec(
      "SELECT device_id FROM devices WHERE device_id = ? AND owner_github_id = ?",
      targetDeviceId, claims.owner
    )];
    if (rows.length === 0) {
      return this._jsonError("device not found", 404);
    }

    this.ctx.storage.sql.exec(
      "UPDATE devices SET revoked = 1 WHERE device_id = ?", targetDeviceId
    );

    return this._jsonResponse({ ok: true });
  }

  // ==================== Response Helpers ====================

  _jsonResponse(data, status = 200) {
    return new Response(JSON.stringify(data), {
      status,
      headers: { "content-type": "application/json" },
    });
  }

  _jsonError(error, status) {
    return new Response(JSON.stringify({ error }), {
      status,
      headers: { "content-type": "application/json" },
    });
  }

  // ==================== Go/Wasm Runtime ====================

  async ensureGo() {
    if (this.goReady) return;
    if (this.goReadyPromise) {
      await this.goReadyPromise;
      return;
    }

    this.goReadyPromise = new Promise((resolve, reject) => {
      const self = this;

      // Send JSON text to a signaling WebSocket.
      globalThis.jsSend = (wsId, jsonStr) => {
        self._sendToWebSocket(wsId, jsonStr);
      };

      // Send binary data to a TURN WebSocket.
      globalThis.jsSendBinary = (wsId, uint8Array) => {
        self._sendBinaryToWebSocket(wsId, uint8Array);
      };

      // Return the TURN secret from SQLite (not env var).
      globalThis.jsTURNSecret = () => {
        return self._getOrCreateTURNSecret();
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
    this._rehydrate();
  }

  // Rebuild Go hub state from WebSocket attachments after hibernation wake.
  _rehydrate() {
    const sockets = this.ctx.getWebSockets();
    let maxWsId = this.nextWsId;
    for (const ws of sockets) {
      const attachment = ws.deserializeAttachment();
      if (!attachment) continue;
      if (attachment.wsId >= maxWsId) {
        maxWsId = attachment.wsId + 1;
      }
      // Only rehydrate signaling peers (not TURN connections — TURN state
      // is transient and does not survive hibernation).
      if (attachment.joined) {
        globalThis.goOnRehydrate(attachment.wsId, attachment.peerId, attachment.publicKey || "", attachment.address || "", JSON.stringify(attachment.routes || []), JSON.stringify(attachment.metadata || {}));
      }
    }
    this.nextWsId = maxWsId;
  }

  // ==================== WebSocket Helpers ====================

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

  _sendBinaryToWebSocket(wsId, uint8Array) {
    const ws = this._findWebSocket(wsId);
    if (ws) {
      try {
        ws.send(uint8Array.buffer.slice(uint8Array.byteOffset, uint8Array.byteOffset + uint8Array.byteLength));
      } catch {
        // WebSocket may be closing; ignore send errors.
      }
    }
  }

  // ==================== DO fetch() ====================

  async fetch(request) {
    const action = request.headers.get("X-Bamgate-Action");

    // --- Unauthenticated auth actions ---
    if (action === "auth-register") {
      return this._handleAuthRegister(request);
    }
    if (action === "auth-refresh") {
      return this._handleAuthRefresh(request);
    }

    // --- JWT-authenticated actions ---
    const jwt = request.headers.get("X-Bamgate-JWT");
    if (!jwt) {
      return this._jsonError("missing authorization", 401);
    }

    const claims = await this._verifyJWT(jwt);
    if (!claims) {
      return this._jsonError("invalid or expired token", 401);
    }

    if (action === "list-devices") {
      return this._handleListDevices(claims);
    }
    if (action === "revoke-device") {
      const targetId = request.headers.get("X-Bamgate-Device-ID");
      return this._handleRevokeDevice(claims, targetId);
    }

    // --- WebSocket upgrade (signaling or TURN) ---
    const upgradeHeader = request.headers.get("Upgrade");
    if (!upgradeHeader || upgradeHeader.toLowerCase() !== "websocket") {
      return new Response("Expected WebSocket upgrade", { status: 426 });
    }

    await this.ensureGo();

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);

    const wsId = this.nextWsId++;
    const isTurn = request.headers.get("X-Bamgate-Turn") === "1";

    this.ctx.acceptWebSocket(server);
    server.serializeAttachment({ wsId, joined: false, isTurn, deviceId: claims.sub });

    return new Response(null, { status: 101, webSocket: client });
  }

  // ==================== WebSocket Hibernation Callbacks ====================

  async webSocketMessage(ws, message) {
    await this.ensureGo();

    const attachment = ws.deserializeAttachment();
    if (!attachment) return;

    const wsId = attachment.wsId;

    // TURN WebSocket connections handle binary STUN/TURN messages.
    if (attachment.isTurn) {
      let data;
      if (typeof message === "string") return; // Unexpected text on TURN.
      if (message instanceof ArrayBuffer) {
        data = new Uint8Array(message);
      } else {
        data = new Uint8Array(message);
      }
      globalThis.goOnTURNMessage(wsId, data);
      return;
    }

    // Signaling WebSocket connections handle JSON text messages.
    if (!attachment.joined) {
      // First message must be a join.
      let msg;
      try {
        msg = JSON.parse(message);
      } catch {
        return;
      }

      if (msg.type !== "join" || !msg.peerId) return;

      ws.serializeAttachment({
        wsId,
        joined: true,
        isTurn: false,
        deviceId: attachment.deviceId,
        peerId: msg.peerId,
        publicKey: msg.publicKey || "",
        address: msg.address || "",
        routes: msg.routes || [],
        metadata: msg.metadata || {},
      });

      // Update last_seen_at for this device.
      if (attachment.deviceId) {
        this._ensureTables();
        const now = Math.floor(Date.now() / 1000);
        this.ctx.storage.sql.exec(
          "UPDATE devices SET last_seen_at = ? WHERE device_id = ?",
          now, attachment.deviceId
        );
      }

      globalThis.goOnJoin(wsId, msg.peerId, msg.publicKey || "", msg.address || "", JSON.stringify(msg.routes || []), JSON.stringify(msg.metadata || {}));
      return;
    }

    // Forward subsequent signaling messages to Go hub for routing.
    globalThis.goOnMessage(wsId, typeof message === "string" ? message : new TextDecoder().decode(message));
  }

  async webSocketClose(ws, code, reason, wasClean) {
    await this.ensureGo();

    const attachment = ws.deserializeAttachment();
    if (!attachment) return;

    if (attachment.isTurn) {
      globalThis.goOnTURNClose(attachment.wsId);
    } else if (attachment.joined) {
      globalThis.goOnLeave(attachment.wsId);
    }

    try {
      ws.close(code, reason);
    } catch {
      // Already closed.
    }
  }

  async webSocketError(ws, error) {
    await this.ensureGo();

    const attachment = ws.deserializeAttachment();
    if (!attachment) return;

    if (attachment.isTurn) {
      globalThis.goOnTURNClose(attachment.wsId);
    } else if (attachment.joined) {
      globalThis.goOnLeave(attachment.wsId);
    }

    try {
      ws.close(1011, "WebSocket error");
    } catch {
      // Already closed.
    }
  }
}
