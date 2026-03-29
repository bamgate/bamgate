import { createHub, type Hub } from "./hub.js";
import { createTurnServer, type TurnServer } from "./turn.js";
import { auth } from "./auth.js";
import type { JWTPayload, WebSocketAttachment, TurnAllocJSON } from "./types.js";

interface Env {
  SIGNALING_ROOM: DurableObjectNamespace;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/status") {
      return new Response(JSON.stringify({ status: "ok" }), {
        headers: { "content-type": "application/json" },
      });
    }

    const roomName = url.searchParams.get("room") || "default";
    const id = env.SIGNALING_ROOM.idFromName(roomName);
    const stub = env.SIGNALING_ROOM.get(id);

    if (url.pathname === "/auth/register" && request.method === "POST") {
      const doReq = new Request(request.url, {
        method: "POST",
        headers: {
          "X-Bamgate-Action": "auth-register",
          "Content-Type": "application/json",
        },
        body: request.body,
      });
      return stub.fetch(doReq);
    }

    if (url.pathname === "/auth/refresh" && request.method === "POST") {
      const doReq = new Request(request.url, {
        method: "POST",
        headers: {
          "X-Bamgate-Action": "auth-refresh",
          "Content-Type": "application/json",
        },
        body: request.body,
      });
      return stub.fetch(doReq);
    }

    const authHeader = request.headers.get("Authorization");
    if (!authHeader || !authHeader.startsWith("Bearer ")) {
      return new Response(JSON.stringify({ error: "missing authorization" }), {
        status: 401,
        headers: { "content-type": "application/json" },
      });
    }
    const token = authHeader.slice(7);
    const authHeaders: string[][] = [["X-Bamgate-JWT", token]];

    if (url.pathname === "/connect") {
      const headers = new Headers([...request.headers.entries(), ...authHeaders]);
      const doReq = new Request(request.url, { method: request.method, headers });
      return stub.fetch(doReq);
    }

    if (url.pathname === "/turn") {
      const headers = new Headers([
        ...request.headers.entries(),
        ...authHeaders,
        ["X-Bamgate-Turn", "1"],
      ]);
      const doReq = new Request(request.url, { method: request.method, headers });
      return stub.fetch(doReq);
    }

    if (url.pathname === "/auth/devices" && request.method === "GET") {
      const doReq = new Request(request.url, {
        method: "GET",
        headers: { "X-Bamgate-Action": "list-devices", "X-Bamgate-JWT": token },
      });
      return stub.fetch(doReq);
    }

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

export class SignalingRoom implements DurableObject {
  private ctx: DurableObjectState;
  private nextWsId = 1;
  private wsMap: Map<number, WebSocket> = new Map();
  private ready = false;
  private readyPromise: Promise<void> | null = null;

  private hub: Hub;
  private turnServer: TurnServer;

  constructor(ctx: DurableObjectState, _env: Env) {
    this.ctx = ctx;

    const sendJson = (wsId: number, data: string): void => {
      const ws = this.wsMap.get(wsId);
      if (ws) {
        try {
          ws.send(data);
        } catch {
          // WebSocket may be closing
        }
      }
    };

    const sendBinary = (wsId: number, data: Uint8Array): void => {
      const ws = this.wsMap.get(wsId);
      if (ws) {
        try {
          const buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);
          ws.send(buf as ArrayBuffer);
        } catch {
          // WebSocket may be closing
        }
      }
    };

    const getTurnSecret = (): string => {
      return this._getOrCreateTURNSecret();
    };

    const saveTurnAlloc = (wsId: number, json: string): void => {
      const ws = this.wsMap.get(wsId);
      if (ws) {
        const attachment = ws.deserializeAttachment() as WebSocketAttachment;
        attachment.turnAlloc = JSON.parse(json) as TurnAllocJSON;
        ws.serializeAttachment(attachment);
      }
    };

    this.hub = createHub(sendJson);
    this.turnServer = createTurnServer(sendBinary, getTurnSecret, saveTurnAlloc);
  }

  async fetch(request: Request): Promise<Response> {
    const action = request.headers.get("X-Bamgate-Action");

    if (action === "auth-register") {
      return this._handleAuthRegister(request);
    }
    if (action === "auth-refresh") {
      return this._handleAuthRefresh(request);
    }

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

    const upgradeHeader = request.headers.get("Upgrade");
    if (!upgradeHeader || upgradeHeader.toLowerCase() !== "websocket") {
      return new Response("Expected WebSocket upgrade", { status: 426 });
    }

    await this.ensureReady();

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair) as [WebSocket, WebSocket];

    const wsId = this.nextWsId++;
    const isTurn = request.headers.get("X-Bamgate-Turn") === "1";

    this.wsMap.set(wsId, server);
    this.ctx.acceptWebSocket(server);
    server.serializeAttachment({
      wsId,
      joined: false,
      isTurn,
      deviceId: claims.sub,
    } as WebSocketAttachment);

    return new Response(null, { status: 101, webSocket: client });
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    await this.ensureReady();

    const attachment = ws.deserializeAttachment() as WebSocketAttachment | null;
    if (!attachment) return;

    const wsId = attachment.wsId;

    if (attachment.isTurn) {
      if (typeof message === "string") return;
      const data = message instanceof ArrayBuffer ? new Uint8Array(message) : new Uint8Array(message);
      this.turnServer.handleMessage(wsId, data);
      return;
    }

    if (!attachment.joined) {
      let msg: { type?: string; peerId?: string; publicKey?: string; address?: string; routes?: string[]; metadata?: Record<string, string> };
      try {
        msg = JSON.parse(typeof message === "string" ? message : new TextDecoder().decode(message));
      } catch {
        return;
      }

      if (msg.type !== "join" || !msg.peerId) return;

      const newAttachment: WebSocketAttachment = {
        wsId,
        joined: true,
        isTurn: false,
        deviceId: attachment.deviceId,
        peerId: msg.peerId,
        publicKey: msg.publicKey || "",
        address: msg.address || "",
        routes: msg.routes || [],
        metadata: msg.metadata || {},
      };
      ws.serializeAttachment(newAttachment);

      if (attachment.deviceId) {
        this._ensureTables();
        const now = Math.floor(Date.now() / 1000);
        this.ctx.storage.sql.exec(
          "UPDATE devices SET last_seen_at = ? WHERE device_id = ?",
          now,
          attachment.deviceId
        );
      }

      const existingPeers = this.hub.join(
        wsId,
        msg.peerId,
        msg.publicKey || "",
        msg.address || "",
        msg.routes || [],
        msg.metadata || {}
      );

      const peersMsg = JSON.stringify({ type: "peers", peers: existingPeers });
      this.hub.message(wsId, peersMsg);
      return;
    }

    const text = typeof message === "string" ? message : new TextDecoder().decode(message);
    this.hub.message(wsId, text);
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string, _wasClean: boolean): Promise<void> {
    await this.ensureReady();

    const attachment = ws.deserializeAttachment() as WebSocketAttachment | null;
    if (!attachment) return;

    const wsId = attachment.wsId;

    if (attachment.isTurn) {
      this.turnServer.close(wsId);
    } else if (attachment.joined) {
      const peerId = this.hub.leave(wsId);
      if (peerId) {
        const leftMsg = JSON.stringify({ type: "peer-left", peerId });
        this.hub.broadcast(wsId, leftMsg);
      }
    }

    this.wsMap.delete(wsId);

    try {
      ws.close(code, reason);
    } catch {
      // Already closed
    }
  }

  async webSocketError(ws: WebSocket, _error: Error): Promise<void> {
    await this.ensureReady();

    const attachment = ws.deserializeAttachment() as WebSocketAttachment | null;
    if (!attachment) return;

    const wsId = attachment.wsId;

    if (attachment.isTurn) {
      this.turnServer.close(wsId);
    } else if (attachment.joined) {
      const peerId = this.hub.leave(wsId);
      if (peerId) {
        const leftMsg = JSON.stringify({ type: "peer-left", peerId });
        this.hub.broadcast(wsId, leftMsg);
      }
    }

    this.wsMap.delete(wsId);

    try {
      ws.close(1011, "WebSocket error");
    } catch {
      // Already closed
    }
  }

  private async ensureReady(): Promise<void> {
    if (this.ready) return;
    if (this.readyPromise) {
      await this.readyPromise;
      return;
    }

    this.readyPromise = (async () => {
      this._rehydrate();
      this.ready = true;
    })();

    await this.readyPromise;
  }

  private _rehydrate(): void {
    const sockets = this.ctx.getWebSockets();
    let maxWsId = this.nextWsId;
    for (const ws of sockets) {
      const attachment = ws.deserializeAttachment() as WebSocketAttachment | null;
      if (!attachment) continue;
      if (attachment.wsId >= maxWsId) {
        maxWsId = attachment.wsId + 1;
      }
      this.wsMap.set(attachment.wsId, ws);

      if (attachment.joined) {
        this.hub.rehydrate(
          attachment.wsId,
          attachment.peerId || "",
          attachment.publicKey || "",
          attachment.address || "",
          attachment.routes || [],
          attachment.metadata || {}
        );
      }
      if (attachment.isTurn && attachment.turnAlloc) {
        this.turnServer.rehydrate(attachment.wsId, JSON.stringify(attachment.turnAlloc));
      }
    }
    this.nextWsId = maxWsId;
  }

  private _ensureTables(): void {
    if ((this as any)._tablesReady) return;

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

    (this as any)._tablesReady = true;
  }

  private _getSubnet(): string {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT value FROM network WHERE key = 'subnet'")];
    if (rows.length > 0) return (rows[0] as { value: string }).value;
    const subnet = "10.0.0.0/24";
    this.ctx.storage.sql.exec("INSERT INTO network (key, value) VALUES ('subnet', ?)", subnet);
    return subnet;
  }

  private _getOrCreateTURNSecret(): string {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT value FROM network WHERE key = 'turn_secret'")];
    if (rows.length > 0) return (rows[0] as { value: string }).value;
    const bytes = new Uint8Array(24);
    crypto.getRandomValues(bytes);
    const hex = Array.from(bytes).map((b) => b.toString(16).padStart(2, "0")).join("");
    const finalSecret = "bg_" + hex;
    this.ctx.storage.sql.exec("INSERT INTO network (key, value) VALUES ('turn_secret', ?)", finalSecret);
    return finalSecret;
  }

  private _getAssignedAddresses(): string[] {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT address FROM devices WHERE revoked = 0 ORDER BY address")];
    return rows.map((r) => (r as { address: string }).address);
  }

  private _assignNextAddress(): string | null {
    const subnet = this._getSubnet();
    const assigned = this._getAssignedAddresses();

    const [baseIP, prefixStr] = subnet.split("/");
    const prefix = parseInt(prefixStr, 10);
    const baseParts = baseIP.split(".").map(Number);
    const baseNum = (baseParts[0] << 24) | (baseParts[1] << 16) | (baseParts[2] << 8) | baseParts[3];
    const hostBits = 32 - prefix;
    const maxHosts = (1 << hostBits) - 2;

    const usedHosts = new Set<number>();
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

  private async _getOrCreateSigningKey(): Promise<{ kid: string; secretHex: string }> {
    this._ensureTables();
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT kid, secret_hex FROM signing_keys WHERE revoked_at IS NULL ORDER BY created_at DESC LIMIT 1"
      ),
    ];
    if (rows.length > 0) {
      return { kid: (rows[0] as { kid: string }).kid, secretHex: (rows[0] as { secret_hex: string }).secret_hex };
    }

    const keyBytes = new Uint8Array(32);
    crypto.getRandomValues(keyBytes);
    const secretHex = Array.from(keyBytes).map((b) => b.toString(16).padStart(2, "0")).join("");

    const kidBytes = new Uint8Array(8);
    crypto.getRandomValues(kidBytes);
    const kid = "k_" + Array.from(kidBytes).map((b) => b.toString(16).padStart(2, "0")).join("");

    const now = Math.floor(Date.now() / 1000);
    this.ctx.storage.sql.exec(
      "INSERT INTO signing_keys (kid, secret_hex, created_at) VALUES (?, ?, ?)",
      kid,
      secretHex,
      now
    );

    return { kid, secretHex };
  }

  private async _signJWT(payload: JWTPayload): Promise<string> {
    const getSigningKey = async () => this._getOrCreateSigningKey();
    return auth.signJWT(payload, getSigningKey);
  }

  private async _verifyJWT(token: string): Promise<JWTPayload | null> {
    const getSigningKey = async (kid: string): Promise<{ kid: string; secretHex: string } | null> => {
      this._ensureTables();
      const rows = [
        ...this.ctx.storage.sql.exec(
          "SELECT kid, secret_hex FROM signing_keys WHERE kid = ? AND revoked_at IS NULL",
          kid
        ),
      ];
      if (rows.length === 0) return null;
      return { kid: (rows[0] as { kid: string }).kid, secretHex: (rows[0] as { secret_hex: string }).secret_hex };
    };
    return auth.verifyJWT(token, getSigningKey);
  }

  private async _handleAuthRegister(request: Request): Promise<Response> {
    let body: { device_name?: string; github_token?: string };
    try {
      body = await request.json();
    } catch {
      return this._jsonError("invalid request body", 400);
    }

    const { device_name, github_token } = body;
    if (!device_name) return this._jsonError("device_name is required", 400);
    if (!github_token) return this._jsonError("github_token is required", 400);

    const ghResp = await fetch("https://api.github.com/user", {
      headers: {
        Authorization: `Bearer ${github_token}`,
        "User-Agent": "bamgate-worker",
        "Accept": "application/vnd.github+json",
      },
    });

    if (!ghResp.ok) {
      return this._jsonError("invalid GitHub token", 401);
    }

    const ghUser = (await ghResp.json()) as { id: number; login: string };
    const githubId = String(ghUser.id);
    const username = ghUser.login;

    this._ensureTables();
    const owners = [...this.ctx.storage.sql.exec("SELECT github_id FROM owners")];

    if (owners.length === 0) {
      const now = Math.floor(Date.now() / 1000);
      this.ctx.storage.sql.exec(
        "INSERT INTO owners (github_id, username, created_at) VALUES (?, ?, ?)",
        githubId,
        username,
        now
      );
    } else if ((owners[0] as { github_id: string }).github_id !== githubId) {
      return this._jsonError("unauthorized: you are not the owner of this network", 403);
    }

    const existing = [
      ...this.ctx.storage.sql.exec(
        "SELECT device_id, address FROM devices WHERE device_name = ? AND owner_github_id = ? AND revoked = 0",
        device_name,
        githubId
      ),
    ];

    const now = Math.floor(Date.now() / 1000);
    const refreshToken = auth.generateRefreshToken();
    const refreshTokenHash = await auth.hashToken(refreshToken);
    const refreshExpiresAt = now + 30 * 24 * 60 * 60;

    let deviceId: string;
    let address: string;

    if (existing.length > 0) {
      deviceId = (existing[0] as { device_id: string }).device_id;
      address = (existing[0] as { address: string }).address;
      this.ctx.storage.sql.exec(
        "UPDATE devices SET refresh_token_hash = ?, refresh_token_expires_at = ?, last_seen_at = ? WHERE device_id = ?",
        refreshTokenHash,
        refreshExpiresAt,
        now,
        deviceId
      );
    } else {
      const newAddress = this._assignNextAddress();
      if (!newAddress) {
        return this._jsonError("no addresses available in subnet", 507);
      }
      address = newAddress;
      deviceId = crypto.randomUUID();
      this.ctx.storage.sql.exec(
        "INSERT INTO devices (device_id, device_name, owner_github_id, address, refresh_token_hash, refresh_token_expires_at, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        deviceId,
        device_name,
        githubId,
        address,
        refreshTokenHash,
        refreshExpiresAt,
        now,
        now
      );
    }

    const turnSecret = this._getOrCreateTURNSecret();
    const subnet = this._getSubnet();

    const accessToken = await this._signJWT({
      sub: deviceId,
      owner: githubId,
      net: "default",
      iat: now,
      exp: now + 3600,
    });

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

  private async _handleAuthRefresh(request: Request): Promise<Response> {
    let body: { device_id?: string; refresh_token?: string };
    try {
      body = await request.json();
    } catch {
      return this._jsonError("invalid request body", 400);
    }

    const { device_id, refresh_token } = body;
    if (!device_id) return this._jsonError("device_id is required", 400);
    if (!refresh_token) return this._jsonError("refresh_token is required", 400);

    this._ensureTables();
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT * FROM devices WHERE device_id = ? AND revoked = 0",
        device_id
      ),
    ];
    if (rows.length === 0) {
      return this._jsonError("device not found or revoked", 401);
    }

    const device = rows[0] as {
      device_id: string;
      owner_github_id: string;
      refresh_token_hash: string;
      refresh_token_expires_at: number;
    };
    const now = Math.floor(Date.now() / 1000);

    if (now > device.refresh_token_expires_at) {
      return this._jsonError("refresh_token_expired", 401);
    }

    const providedHash = await auth.hashToken(refresh_token);
    if (providedHash !== device.refresh_token_hash) {
      return this._jsonError("invalid refresh token", 401);
    }

    const newRefreshToken = auth.generateRefreshToken();
    const newHash = await auth.hashToken(newRefreshToken);
    const newExpiresAt = now + 30 * 24 * 60 * 60;

    this.ctx.storage.sql.exec(
      "UPDATE devices SET refresh_token_hash = ?, refresh_token_expires_at = ?, last_seen_at = ? WHERE device_id = ?",
      newHash,
      newExpiresAt,
      now,
      device_id
    );

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

  private async _handleListDevices(claims: JWTPayload): Promise<Response> {
    this._ensureTables();
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT device_id, device_name, address, created_at, last_seen_at, revoked FROM devices WHERE owner_github_id = ? ORDER BY created_at",
        claims.owner
      ),
    ];

    return this._jsonResponse({
      devices: rows.map((r) => ({
        device_id: (r as { device_id: string }).device_id,
        device_name: (r as { device_name: string }).device_name,
        address: (r as { address: string }).address,
        created_at: (r as { created_at: number }).created_at,
        last_seen_at: (r as { last_seen_at: number | null }).last_seen_at,
        revoked: (r as { revoked: number }).revoked === 1,
      })),
    });
  }

  private async _handleRevokeDevice(claims: JWTPayload, targetDeviceId: string | null): Promise<Response> {
    if (!targetDeviceId) {
      return this._jsonError("device_id is required", 400);
    }

    if (claims.sub === targetDeviceId) {
      return this._jsonError("cannot revoke your own device", 400);
    }

    this._ensureTables();
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT device_id FROM devices WHERE device_id = ? AND owner_github_id = ?",
        targetDeviceId,
        claims.owner
      ),
    ];
    if (rows.length === 0) {
      return this._jsonError("device not found", 404);
    }

    this.ctx.storage.sql.exec("UPDATE devices SET revoked = 1 WHERE device_id = ?", targetDeviceId);

    return this._jsonResponse({ ok: true });
  }

  private _jsonResponse(data: unknown, status = 200): Response {
    return new Response(JSON.stringify(data), {
      status,
      headers: { "content-type": "application/json" },
    });
  }

  private _jsonError(error: string, status: number): Response {
    return new Response(JSON.stringify({ error }), {
      status,
      headers: { "content-type": "application/json" },
    });
  }
}
