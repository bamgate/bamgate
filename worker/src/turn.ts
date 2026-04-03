import * as stun from "./stun.js";
import type { TurnAllocJSON } from "./types.js";

const TURN_REALM = "bamgate";
const DEFAULT_ALLOC_LIFETIME = 600;
const MAX_ALLOC_LIFETIME = 3600;
const RELAY_BASE_IP = "10.255.0.0";
const RELAY_BASE_PORT = 50000;
const MAX_RELAY_HOST = 255;

interface Allocation {
  wsId: number;
  username: string;
  nonce: string;
  authKey: Uint8Array;
  relayAddr: { ip: Uint8Array; port: number };
  createdAt: number;
  lifetime: number;
  permissions: Set<string>;
  channels: Map<number, string>;
  channelByAddr: Map<string, number>;
}

export interface SendBinaryFn {
  (wsId: number, data: Uint8Array): void;
}

export interface GetTURNSecretFn {
  (): string;
}

export interface SaveTURNAllocFn {
  (wsId: number, json: string): void;
}

export class TurnServer {
  private allocations: Map<number, Allocation> = new Map();
  private relayByAddr: Map<string, number> = new Map();
  private nextRelayHost: number = 1;
  private sendBinaryFn: SendBinaryFn;
  private getTURNSecretFn: GetTURNSecretFn;
  private saveTURNAllocFn: SaveTURNAllocFn;

  constructor(
    sendBinaryFn: SendBinaryFn,
    getTURNSecretFn: GetTURNSecretFn,
    saveTURNAllocFn: SaveTURNAllocFn
  ) {
    this.sendBinaryFn = sendBinaryFn;
    this.getTURNSecretFn = getTURNSecretFn;
    this.saveTURNAllocFn = saveTURNAllocFn;
  }

  private generateNonce(): string {
    return String(Date.now() * 1000000 + Math.floor(Math.random() * 1000000));
  }

  private getTURNSecret(): string {
    return this.getTURNSecretFn();
  }

  private validateTURNCredentials(username: string, password: string): Error | null {
    const secret = this.getTURNSecret();
    if (!secret) {
      return new Error("TURN_SECRET not configured");
    }

    const parts = username.split(":");
    if (parts.length !== 2) {
      return new Error("invalid username format");
    }

    const expiry = parseInt(parts[0], 10);
    if (isNaN(expiry)) {
      return new Error("invalid expiry in username");
    }

    if (Math.floor(Date.now() / 1000) > expiry) {
      return new Error("credentials expired");
    }

    const mac = this.computeHMACSHA1(secret, username);
    const expected = this.base64Encode(mac);

    if (password !== expected) {
      return new Error("invalid password");
    }

    return null;
  }

  private computeHMACSHA1(key: string, data: string): Uint8Array {
    const cryptoKey = new TextEncoder().encode(key);
    const msgData = new TextEncoder().encode(data);

    const ipad = new Uint8Array(64);
    const opad = new Uint8Array(64);
    for (let i = 0; i < 64; i++) {
      ipad[i] = i < cryptoKey.length ? cryptoKey[i] ^ 0x36 : 0x36;
      opad[i] = i < cryptoKey.length ? cryptoKey[i] ^ 0x5c : 0x5c;
    }

    const innerData = new Uint8Array(64 + msgData.length);
    innerData.set(ipad, 0);
    innerData.set(msgData, 64);

    return this.sha1(innerData);
  }

  private sha1(data: Uint8Array): Uint8Array {
    const h = [0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476];

    const padded = new Uint8Array(((data.length + 8) & ~63) + 64);
    padded.set(data);
    padded[data.length] = 0x80;

    const view = new DataView(padded.buffer, padded.byteOffset + padded.length - 8, 8);
    view.setUint32(4, data.length * 8, false);

    for (let chunk = 0; chunk < padded.length; chunk += 64) {
      const w = new Uint32Array(80);
      const wView = new DataView(padded.buffer, padded.byteOffset + chunk, 64);
      for (let i = 0; i < 16; i++) {
        w[i] = wView.getUint32(i * 4, false);
      }
      for (let i = 16; i < 80; i++) {
        const v = w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16];
        w[i] = (v << 1) | (v >>> 31);
      }

      let a = h[0], b = h[1], c = h[2], d = h[3];

      for (let i = 0; i < 80; i++) {
        let f: number, k: number;
        if (i < 20) {
          f = (b & c) | (~b & d);
          k = 0x5a827999;
        } else if (i < 40) {
          f = b ^ c ^ d;
          k = 0x6ed9eba1;
        } else if (i < 60) {
          f = (b & c) | (b & d) | (c & d);
          k = 0x8f1bbcdc;
        } else {
          f = b ^ c ^ d;
          k = 0xca62c1d6;
        }

        const temp = ((a << 5) | (a >>> 27)) + f + k + w[i];
        a = temp >>> 0;
        const temp2 = d;
        d = c;
        c = (b << 30) | (b >>> 2);
        b = a;
        a = temp2;
      }

      h[0] = (h[0] + a) >>> 0;
      h[1] = (h[1] + b) >>> 0;
      h[2] = (h[2] + c) >>> 0;
      h[3] = (h[3] + d) >>> 0;
    }

    const result = new Uint8Array(20);
    const resultView = new DataView(result.buffer, result.byteOffset, 20);
    resultView.setUint32(0, h[0], false);
    resultView.setUint32(4, h[1], false);
    resultView.setUint32(8, h[2], false);
    resultView.setUint32(12, h[3], false);
    return result;
  }

  private base64Encode(data: Uint8Array): string {
    let binary = "";
    for (let i = 0; i < data.length; i++) {
      binary += String.fromCharCode(data[i]);
    }
    return btoa(binary);
  }

  private recomputePassword(username: string): string {
    const secret = this.getTURNSecret();
    const mac = this.computeHMACSHA1(secret, username);
    return this.base64Encode(mac);
  }

  private deriveAuthKey(username: string, realm: string, password: string): Uint8Array {
    return stun.deriveAuthKey(username, realm, password);
  }

  private assignRelayAddr(): { ip: Uint8Array; port: number } | null {
    if (this.nextRelayHost > MAX_RELAY_HOST) {
      return null;
    }

    const ip = stun.parseIPv4(RELAY_BASE_IP);
    if (!ip) return null;

    const relayIp = new Uint8Array(4);
    relayIp[0] = ip[0];
    relayIp[1] = ip[1];
    relayIp[2] = ip[2];
    relayIp[3] = this.nextRelayHost;
    this.nextRelayHost++;

    return { ip: relayIp, port: RELAY_BASE_PORT };
  }

  private addrKey(addr: { ip: Uint8Array; port: number }): string {
    return `${stun.ipToString(addr.ip)}:${addr.port}`;
  }

  handleMessage(wsId: number, data: Uint8Array): void {
    if (stun.isChannelData(data)) {
      this.handleChannelData(wsId, data);
      return;
    }

    if (!stun.isSTUN(data)) {
      return;
    }

    let msg: stun.Message;
    try {
      msg = stun.parse(data);
    } catch {
      return;
    }

    switch (msg.method) {
      case stun.Method.Binding:
        this.handleBinding(wsId, msg);
        break;
      case stun.Method.Allocate:
        this.handleAllocate(wsId, msg, data);
        break;
      case stun.Method.Refresh:
        this.handleRefresh(wsId, msg, data);
        break;
      case stun.Method.CreatePermission:
        this.handleCreatePermission(wsId, msg, data);
        break;
      case stun.Method.ChannelBind:
        this.handleChannelBind(wsId, msg, data);
        break;
      case stun.Method.Send:
        this.handleSend(wsId, msg);
        break;
    }
  }

  private async handleBinding(wsId: number, msg: stun.Message): Promise<void> {
    const relayAddr = { ip: stun.parseIPv4("127.0.0.1")!, port: 1234 };
    const response = stun
      .newResponse(msg, stun.Class.SuccessResponse)
      .addXORAddress(stun.Attr.XORMappedAddress, relayAddr)
      .buildAsync(null);
    this.sendBinaryFn(wsId, await response);
  }

  private async handleAllocate(
    wsId: number,
    msg: stun.Message,
    rawData: Uint8Array
  ): Promise<void> {
    const existing = this.allocations.get(wsId);
    const username = stun.getUsername(msg);

    if (!username) {
      const nonce = this.generateNonce();
      if (!existing) {
        this.allocations.set(wsId, {
          wsId,
          username: "",
          nonce,
          authKey: new Uint8Array(0),
          relayAddr: { ip: new Uint8Array(0), port: 0 },
          createdAt: 0,
          lifetime: 0,
          permissions: new Set(),
          channels: new Map(),
          channelByAddr: new Map(),
        });
      } else {
        existing.nonce = nonce;
      }

      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(401, "Unauthorized")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    if (!existing) {
      const nonce = this.generateNonce();
      this.allocations.set(wsId, {
        wsId,
        username,
        nonce,
        authKey: new Uint8Array(0),
        relayAddr: { ip: new Uint8Array(0), port: 0 },
        createdAt: 0,
        lifetime: 0,
        permissions: new Set(),
        channels: new Map(),
        channelByAddr: new Map(),
      });

      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(401, "Unauthorized")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const password = this.recomputePassword(username);
    const credError = this.validateTURNCredentials(username, password);
    if (credError) {
      const nonce = this.generateNonce();
      existing.nonce = nonce;
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(401, "Unauthorized")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const authKey = this.deriveAuthKey(username, TURN_REALM, password);
    const integrityError = await stun.checkIntegrity(rawData, authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      existing.nonce = nonce;
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(401, "Unauthorized")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    if (existing.relayAddr.ip.length > 0) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(437, "Allocation Mismatch")
        .buildAsync(authKey);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const relayAddr = this.assignRelayAddr();
    if (!relayAddr) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(508, "Insufficient Capacity")
        .buildAsync(authKey);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const lifetime = DEFAULT_ALLOC_LIFETIME;
    existing.username = username;
    existing.authKey = authKey;
    existing.relayAddr = relayAddr;
    existing.createdAt = Math.floor(Date.now() / 1000);
    existing.lifetime = lifetime;

    this.relayByAddr.set(this.addrKey(relayAddr), wsId);

    const mappedAddr = { ip: stun.parseIPv4("127.0.0.1")!, port: 1234 };
    const response = stun
      .newResponse(msg, stun.Class.SuccessResponse)
      .addXORAddress(stun.Attr.XORRelayedAddress, relayAddr)
      .addXORAddress(stun.Attr.XORMappedAddress, mappedAddr)
      .addLifetime(lifetime)
      .buildAsync(authKey);

    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }

  private async handleRefresh(
    wsId: number,
    msg: stun.Message,
    rawData: Uint8Array
  ): Promise<void> {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(437, "Allocation Mismatch")
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const integrityError = await stun.checkIntegrity(rawData, alloc.authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      alloc.nonce = nonce;
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(438, "Stale Nonce")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const requestedLifetime = stun.getLifetime(msg);

    if (requestedLifetime === 0) {
      const response = stun
        .newResponse(msg, stun.Class.SuccessResponse)
        .addLifetime(0)
        .buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response);
      this.removeAllocation(wsId);
      return;
    }

    const lifetime = Math.min(requestedLifetime, MAX_ALLOC_LIFETIME);
    alloc.lifetime = lifetime;

    const response = stun
      .newResponse(msg, stun.Class.SuccessResponse)
      .addLifetime(lifetime)
      .buildAsync(alloc.authKey);

    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }

  private async handleCreatePermission(
    wsId: number,
    msg: stun.Message,
    rawData: Uint8Array
  ): Promise<void> {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(437, "Allocation Mismatch")
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const integrityError = await stun.checkIntegrity(rawData, alloc.authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      alloc.nonce = nonce;
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(438, "Stale Nonce")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const addrs = stun.getXORPeerAddresses(msg);
    for (const addr of addrs) {
      alloc.permissions.add(stun.ipToString(addr.ip));
    }

    const response = stun
      .newResponse(msg, stun.Class.SuccessResponse)
      .buildAsync(alloc.authKey);

    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }

  private async handleChannelBind(
    wsId: number,
    msg: stun.Message,
    rawData: Uint8Array
  ): Promise<void> {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(437, "Allocation Mismatch")
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const integrityError = await stun.checkIntegrity(rawData, alloc.authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      alloc.nonce = nonce;
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(438, "Stale Nonce")
        .addRealm(TURN_REALM)
        .addNonce(nonce)
        .buildAsync(null);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const channelNumber = stun.getChannelNumber(msg);
    if (channelNumber < 0x4000 || channelNumber > 0x7fff) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(400, "Bad Request")
        .buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const peerAddr = stun.getXORPeerAddress(msg);
    if (!peerAddr || peerAddr.ip.length === 0) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(400, "Bad Request")
        .buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    const peerKey = this.addrKey(peerAddr);

    const existingAddr = alloc.channels.get(channelNumber);
    if (existingAddr !== undefined && existingAddr !== peerKey) {
      const response = stun
        .newResponse(msg, stun.Class.ErrorResponse)
        .addErrorCode(400, "Bad Request")
        .buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response);
      return;
    }

    alloc.channels.set(channelNumber, peerKey);
    alloc.channelByAddr.set(peerKey, channelNumber);
    alloc.permissions.add(stun.ipToString(peerAddr.ip));

    const response = stun
      .newResponse(msg, stun.Class.SuccessResponse)
      .buildAsync(alloc.authKey);

    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }

  private async handleSend(wsId: number, msg: stun.Message): Promise<void> {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      return;
    }

    const peerAddr = stun.getXORPeerAddress(msg);
    if (!peerAddr || peerAddr.ip.length === 0) {
      return;
    }

    const data = stun.getData(msg);
    if (!data) {
      return;
    }

    const peerIpStr = stun.ipToString(peerAddr.ip);
    if (!alloc.permissions.has(peerIpStr)) {
      return;
    }

    const targetKey = this.addrKey(peerAddr);
    const targetWsId = this.relayByAddr.get(targetKey);
    if (targetWsId === undefined) {
      return;
    }

    const targetAlloc = this.allocations.get(targetWsId);
    if (!targetAlloc) {
      return;
    }

    const ourKey = this.addrKey(alloc.relayAddr);
    const channelNum = targetAlloc.channelByAddr.get(ourKey);
    if (channelNum !== undefined) {
      const frame = stun.buildChannelData(channelNum, data);
      this.sendBinaryFn(targetWsId, frame);
      return;
    }

    const txID = new Uint8Array(12);
    const ind = stun
      .newBuilder(stun.Method.Data, stun.Class.Indication, txID)
      .addXORAddress(stun.Attr.XORPeerAddress, alloc.relayAddr)
      .addData(data)
      .buildNoFingerprintAsync(null);

    this.sendBinaryFn(targetWsId, await ind);
  }

  private async handleChannelData(wsId: number, data: Uint8Array): Promise<void> {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      return;
    }

    const cd = stun.parseChannelData(data);
    const peerKey = alloc.channels.get(cd.channelNumber);
    if (!peerKey) {
      return;
    }

    const targetWsId = this.relayByAddr.get(peerKey);
    if (targetWsId === undefined) {
      return;
    }

    const targetAlloc = this.allocations.get(targetWsId);
    if (!targetAlloc) {
      return;
    }

    const ourKey = this.addrKey(alloc.relayAddr);
    const channelNum = targetAlloc.channelByAddr.get(ourKey);
    if (channelNum !== undefined) {
      const frame = stun.buildChannelData(channelNum, cd.data);
      this.sendBinaryFn(targetWsId, frame);
      return;
    }

    const txID = new Uint8Array(12);
    const ind = stun
      .newBuilder(stun.Method.Data, stun.Class.Indication, txID)
      .addXORAddress(stun.Attr.XORPeerAddress, alloc.relayAddr)
      .addData(cd.data)
      .buildNoFingerprintAsync(null);

    this.sendBinaryFn(targetWsId, await ind);
  }

  close(wsId: number): void {
    this.removeAllocation(wsId);
  }

  private removeAllocation(wsId: number): void {
    const alloc = this.allocations.get(wsId);
    if (!alloc) return;

    if (alloc.relayAddr.ip.length > 0) {
      this.relayByAddr.delete(this.addrKey(alloc.relayAddr));
    }
    this.allocations.delete(wsId);
  }

  rehydrate(wsId: number, jsonStr: string): void {
    const j: TurnAllocJSON = JSON.parse(jsonStr);

    const authKey = this.base64Decode(j.ak);
    const relayIp = stun.parseIPv4(j.ri);
    if (!relayIp) return;

    const relayAddr = { ip: relayIp, port: j.rp };

    const permissions = new Set<string>();
    if (j.pm) {
      for (const ip of j.pm) {
        permissions.add(ip);
      }
    }

    const channels = new Map<number, string>();
    const channelByAddr = new Map<string, number>();
    if (j.ch) {
      for (const [numStr, addr] of Object.entries(j.ch)) {
        const num = parseInt(numStr, 10);
        channels.set(num, addr);
        channelByAddr.set(addr, num);
      }
    }

    const alloc: Allocation = {
      wsId,
      username: j.u,
      nonce: j.n,
      authKey,
      relayAddr,
      createdAt: j.ca,
      lifetime: j.lt,
      permissions,
      channels,
      channelByAddr,
    };

    this.allocations.set(wsId, alloc);
    this.relayByAddr.set(this.addrKey(relayAddr), wsId);

    if (j.rh >= this.nextRelayHost) {
      this.nextRelayHost = j.rh + 1;
    }
  }

  private base64Decode(str: string): Uint8Array {
    while (str.length % 4 !== 0) str += "=";
    const binary = atob(str.replace(/-/g, "+").replace(/_/g, "/"));
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  }

  private saveAllocation(wsId: number): void {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0 || alloc.relayAddr.ip.length === 0) {
      return;
    }

    const perms: string[] = [];
    for (const ip of alloc.permissions) {
      perms.push(ip);
    }

    const channels: Record<string, string> = {};
    for (const [num, addr] of alloc.channels) {
      channels[String(num)] = addr;
    }

    const j: TurnAllocJSON = {
      u: alloc.username,
      n: alloc.nonce,
      ak: this.base64Encode(alloc.authKey),
      ri: stun.ipToString(alloc.relayAddr.ip),
      rp: alloc.relayAddr.port,
      rh: alloc.relayAddr.ip[3],
      ca: alloc.createdAt,
      lt: alloc.lifetime,
      pm: perms,
      ch: channels,
    };

    this.saveTURNAllocFn(wsId, JSON.stringify(j));
  }
}

export function createTurnServer(
  sendBinaryFn: SendBinaryFn,
  getTURNSecretFn: GetTURNSecretFn,
  saveTURNAllocFn: SaveTURNAllocFn
): TurnServer {
  return new TurnServer(sendBinaryFn, getTURNSecretFn, saveTURNAllocFn);
}
