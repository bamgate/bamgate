const HEADER_SIZE = 20;
const MAGIC_COOKIE = 0x2112a442;
const FINGERPRINT_XOR = 0x5354554e;

export const Method = {
  Binding: 0x001,
  Allocate: 0x003,
  Refresh: 0x004,
  Send: 0x006,
  Data: 0x007,
  CreatePermission: 0x008,
  ChannelBind: 0x009,
} as const;

export const Class = {
  Request: 0x00,
  Indication: 0x01,
  SuccessResponse: 0x02,
  ErrorResponse: 0x03,
} as const;

export const Attr = {
  MappedAddress: 0x0001,
  Username: 0x0006,
  MessageIntegrity: 0x0008,
  ErrorCode: 0x0009,
  ChannelNumber: 0x000c,
  Lifetime: 0x000d,
  XORPeerAddress: 0x0012,
  Data: 0x0013,
  Realm: 0x0014,
  Nonce: 0x0015,
  XORRelayedAddress: 0x0016,
  RequestedTransport: 0x0019,
  XORMappedAddress: 0x0020,
  Fingerprint: 0x8028,
  Software: 0x8022,
} as const;

export const FamilyIPv4 = 0x01;
export const FamilyIPv6 = 0x02;

export type MethodType = (typeof Method)[keyof typeof Method];
export type ClassType = (typeof Class)[keyof typeof Class];

function messageType(method: number, classVal: number): number {
  const m = method & 0xfff;
  const c = classVal & 0x03;
  return (m & 0x0f) |
    ((c & 0x01) << 4) |
    ((m & 0x70) << 1) |
    ((c & 0x02) << 7) |
    ((m & 0xf80) << 2);
}

export function parseType(t: number): { method: number; class: number } {
  const method =
    (t & 0x0f) |
    ((t >> 1) & 0x70) |
    ((t >> 2) & 0xf80);
  const classVal = ((t >> 4) & 0x01) | ((t >> 7) & 0x02);
  return { method, class: classVal };
}

export interface XORAddress {
  ip: Uint8Array;
  port: number;
}

export interface Message {
  method: number;
  class: number;
  transactionID: Uint8Array;
  attributes: Array<{ type: number; value: Uint8Array }>;
}

export interface ChannelData {
  channelNumber: number;
  data: Uint8Array;
}

export function isChannelData(data: Uint8Array): boolean {
  if (data.length < 4) return false;
  const view = new DataView(data.buffer, data.byteOffset, 2);
  const ch = view.getUint16(0, false);
  return ch >= 0x4000 && ch <= 0x7fff;
}

export function isSTUN(data: Uint8Array): boolean {
  if (data.length < HEADER_SIZE) return false;
  if (data[0] & 0xc0) return false;
  const view = new DataView(data.buffer, data.byteOffset + 4, 4);
  const cookie = view.getUint32(0, false);
  return cookie === MAGIC_COOKIE;
}

export function parseChannelData(data: Uint8Array): ChannelData {
  const view = new DataView(data.buffer, data.byteOffset, 4);
  const channelNumber = view.getUint16(0, false);
  const length = view.getUint16(2, false);
  const payload = new Uint8Array(data.buffer, data.byteOffset + 4, length);
  return { channelNumber, data: payload };
}

export function buildChannelData(channelNumber: number, payload: Uint8Array): Uint8Array {
  const paddedLen = (payload.length + 3) & ~3;
  const buf = new Uint8Array(4 + paddedLen);
  const view = new DataView(buf.buffer, buf.byteOffset, 4);
  view.setUint16(0, channelNumber, false);
  view.setUint16(2, payload.length, false);
  buf.set(payload, 4);
  return buf;
}

export function parse(data: Uint8Array): Message {
  if (data.length < HEADER_SIZE) {
    throw new Error(`message too short: ${data.length} bytes`);
  }

  const view = new DataView(data.buffer, data.byteOffset, 20);
  const msgType = view.getUint16(0, false);
  const msgLen = view.getUint16(2, false);
  const cookie = view.getUint32(4, false);

  if (cookie !== MAGIC_COOKIE) {
    throw new Error(`bad magic cookie: 0x${cookie.toString(16)}`);
  }

  if (HEADER_SIZE + msgLen > data.length) {
    throw new Error(
      `message length ${msgLen} exceeds available ${data.length - HEADER_SIZE}`
    );
  }

  const { method, class: classVal } = parseType(msgType);

  const transactionID = new Uint8Array(12);
  transactionID.set(data.subarray(8, 20));

  const attributes: Array<{ type: number; value: Uint8Array }> = [];
  let offset = HEADER_SIZE;
  const end = HEADER_SIZE + msgLen;

  while (offset + 4 <= end) {
    const attrView = new DataView(data.buffer, data.byteOffset + offset, 4);
    const attrType = attrView.getUint16(0, false);
    const attrLen = attrView.getUint16(2, false);

    if (offset + 4 + attrLen > end) {
      throw new Error(
        `attribute 0x${attrType.toString(16)} length ${attrLen} exceeds message`
      );
    }

    const value = new Uint8Array(data.buffer, data.byteOffset + offset + 4, attrLen);
    attributes.push({ type: attrType, value });

    const paddedLen = (attrLen + 3) & ~3;
    offset += 4 + paddedLen;
  }

  return { method, class: classVal, transactionID, attributes };
}

function getAttr(msg: Message, attrType: number): Uint8Array | null {
  for (const attr of msg.attributes) {
    if (attr.type === attrType) {
      return attr.value;
    }
  }
  return null;
}

function getAttrs(msg: Message, attrType: number): Uint8Array[] {
  const result: Uint8Array[] = [];
  for (const attr of msg.attributes) {
    if (attr.type === attrType) {
      result.push(attr.value);
    }
  }
  return result;
}

export function getUsername(msg: Message): string {
  const v = getAttr(msg, Attr.Username);
  return v ? new TextDecoder().decode(v) : "";
}

export function getRealm(msg: Message): string {
  const v = getAttr(msg, Attr.Realm);
  return v ? new TextDecoder().decode(v) : "";
}

export function getNonce(msg: Message): string {
  const v = getAttr(msg, Attr.Nonce);
  return v ? new TextDecoder().decode(v) : "";
}

export function getLifetime(msg: Message): number {
  const v = getAttr(msg, Attr.Lifetime);
  if (!v || v.length < 4) return 0;
  const view = new DataView(v.buffer, v.byteOffset, 4);
  return view.getUint32(0, false);
}

export function getRequestedTransport(msg: Message): number {
  const v = getAttr(msg, Attr.RequestedTransport);
  return v && v.length > 0 ? v[0] : 0;
}

export function getChannelNumber(msg: Message): number {
  const v = getAttr(msg, Attr.ChannelNumber);
  if (!v || v.length < 2) return 0;
  const view = new DataView(v.buffer, v.byteOffset, 2);
  return view.getUint16(0, false);
}

export function getData(msg: Message): Uint8Array | null {
  return getAttr(msg, Attr.Data);
}

function decodeXORAddress(value: Uint8Array, txID: Uint8Array): XORAddress {
  if (value.length < 4) {
    return { ip: new Uint8Array(0), port: 0 };
  }

  const family = value[1];
  const view = new DataView(value.buffer, value.byteOffset + 2, 2);
  const xorPort = view.getUint16(0, false);
  const port = xorPort ^ (MAGIC_COOKIE >> 16);

  let ip: Uint8Array;

  if (family === FamilyIPv4) {
    if (value.length < 8) {
      return { ip: new Uint8Array(0), port: 0 };
    }
    ip = new Uint8Array(4);
    for (let i = 0; i < 4; i++) {
      ip[i] = value[4 + i] ^ ((MAGIC_COOKIE >> (24 - i * 8)) & 0xff);
    }
  } else if (family === FamilyIPv6) {
    if (value.length < 20) {
      return { ip: new Uint8Array(0), port: 0 };
    }
    ip = new Uint8Array(16);
    for (let i = 0; i < 4; i++) {
      ip[i] = value[4 + i] ^ ((MAGIC_COOKIE >> (24 - i * 8)) & 0xff);
    }
    for (let i = 0; i < 12; i++) {
      ip[4 + i] = value[8 + i] ^ txID[i];
    }
  } else {
    return { ip: new Uint8Array(0), port: 0 };
  }

  return { ip, port };
}

export function getXORPeerAddress(msg: Message): XORAddress | null {
  const v = getAttr(msg, Attr.XORPeerAddress);
  return v ? decodeXORAddress(v, msg.transactionID) : null;
}

export function getXORPeerAddresses(msg: Message): XORAddress[] {
  const vals = getAttrs(msg, Attr.XORPeerAddress);
  return vals.map((v) => decodeXORAddress(v, msg.transactionID));
}

export function getXORRelayedAddress(msg: Message): XORAddress | null {
  const v = getAttr(msg, Attr.XORRelayedAddress);
  return v ? decodeXORAddress(v, msg.transactionID) : null;
}

export function getErrorCode(msg: Message): { code: number; reason: string } | null {
  const v = getAttr(msg, Attr.ErrorCode);
  if (!v || v.length < 4) return null;
  const classDigit = v[2];
  const numberDigit = v[3];
  const code = classDigit * 100 + numberDigit;
  const reason = new TextDecoder().decode(v.subarray(4));
  return { code, reason };
}

export class Builder {
  private method: number;
  private class: number;
  private txID: Uint8Array;
  private attrs: Uint8Array[] = [];

  constructor(method: number, classVal: number, txID: Uint8Array) {
    this.method = method;
    this.class = classVal;
    this.txID = txID;
  }

  addRaw(attrType: number, value: Uint8Array): this {
    const header = new Uint8Array(4);
    const view = new DataView(header.buffer, header.byteOffset, 4);
    view.setUint16(0, attrType, false);
    view.setUint16(2, value.length, false);
    this.attrs.push(header);

    this.attrs.push(value);

    const pad = (4 - (value.length % 4)) % 4;
    if (pad > 0) {
      this.attrs.push(new Uint8Array(pad));
    }

    return this;
  }

  addString(attrType: number, s: string): this {
    return this.addRaw(attrType, new TextEncoder().encode(s));
  }

  addUsername(username: string): this {
    return this.addString(Attr.Username, username);
  }

  addRealm(realm: string): this {
    return this.addString(Attr.Realm, realm);
  }

  addNonce(nonce: string): this {
    return this.addString(Attr.Nonce, nonce);
  }

  addLifetime(seconds: number): this {
    const v = new Uint8Array(4);
    const view = new DataView(v.buffer, v.byteOffset, 4);
    view.setUint32(0, seconds, false);
    return this.addRaw(Attr.Lifetime, v);
  }

  addErrorCode(code: number, reason: string): this {
    const value = new Uint8Array(4 + new TextEncoder().encode(reason).length);
    const view = new DataView(value.buffer, value.byteOffset, 4);
    view.setUint8(2, Math.floor(code / 100));
    view.setUint8(3, code % 100);
    const reasonBytes = new TextEncoder().encode(reason);
    value.set(reasonBytes, 4);
    return this.addRaw(Attr.ErrorCode, value);
  }

  addXORAddress(attrType: number, addr: XORAddress): this {
    const ip4 = addr.ip.length === 4 ? addr.ip : null;
    const ip6 = addr.ip.length === 16 ? addr.ip : null;

    if (ip4) {
      const value = new Uint8Array(8);
      value[1] = FamilyIPv4;
      const view = new DataView(value.buffer, value.byteOffset + 2, 2);
      view.setUint16(0, addr.port ^ (MAGIC_COOKIE >> 16), false);
      for (let i = 0; i < 4; i++) {
        value[4 + i] = ip4[i] ^ ((MAGIC_COOKIE >> (24 - i * 8)) & 0xff);
      }
      return this.addRaw(attrType, value);
    }

    if (ip6) {
      const value = new Uint8Array(20);
      value[1] = FamilyIPv6;
      const view = new DataView(value.buffer, value.byteOffset + 2, 2);
      view.setUint16(0, addr.port ^ (MAGIC_COOKIE >> 16), false);
      for (let i = 0; i < 4; i++) {
        value[4 + i] = ip6[i] ^ ((MAGIC_COOKIE >> (24 - i * 8)) & 0xff);
      }
      for (let i = 0; i < 12; i++) {
        value[8 + i] = ip6[4 + i] ^ this.txID[i];
      }
      return this.addRaw(attrType, value);
    }

    return this;
  }

  addData(data: Uint8Array): this {
    return this.addRaw(Attr.Data, data);
  }

  addChannelNumber(ch: number): this {
    const v = new Uint8Array(4);
    const view = new DataView(v.buffer, v.byteOffset, 4);
    view.setUint16(0, ch, false);
    return this.addRaw(Attr.ChannelNumber, v);
  }

  async buildAsync(authKey: Uint8Array | null): Promise<Uint8Array> {
    const attrsLen = this.attrs.reduce((sum, a) => sum + a.length, 0);

    let totalLen = attrsLen;
    if (authKey) {
      totalLen += 24 + 8;
    } else {
      totalLen += 8;
    }

    const buf = new Uint8Array(HEADER_SIZE + totalLen);
    const view = new DataView(buf.buffer, buf.byteOffset, HEADER_SIZE);

    view.setUint16(0, messageType(this.method, this.class), false);
    view.setUint32(4, MAGIC_COOKIE, false);
    buf.set(this.txID, 8);

    let offset = HEADER_SIZE;
    for (const attr of this.attrs) {
      buf.set(attr, offset);
      offset += attr.length;
    }

    if (authKey) {
      view.setUint16(2, offset - HEADER_SIZE + 24, false);
      const miOffset = offset;

      const hashData = new Uint8Array(miOffset);
      hashData.set(buf.subarray(0, miOffset));
      const hashDataView = new DataView(hashData.buffer, hashData.byteOffset + 2, 2);
      hashDataView.setUint16(0, miOffset - HEADER_SIZE + 24);

      const hmac = await hmacSha1(authKey, hashData);

      const miHeader = new Uint8Array(4);
      const miHeaderView = new DataView(miHeader.buffer, miHeader.byteOffset, 4);
      miHeaderView.setUint16(0, Attr.MessageIntegrity);
      miHeaderView.setUint16(2, 20, false);
      buf.set(miHeader, offset);
      offset += 4;
      buf.set(hmac, offset);
      offset += 20;
    }

    view.setUint16(2, offset - HEADER_SIZE, false);

    const crc = (crc32IEEE(buf.subarray(0, offset)) ^ FINGERPRINT_XOR) >>> 0;
    const fpHeader = new Uint8Array(4);
    const fpHeaderView = new DataView(fpHeader.buffer, fpHeader.byteOffset, 4);
    fpHeaderView.setUint16(0, Attr.Fingerprint);
    fpHeaderView.setUint16(2, 4, false);
    buf.set(fpHeader, offset);
    offset += 4;

    const fpValue = new Uint8Array(4);
    const fpValueView = new DataView(fpValue.buffer, fpValue.byteOffset, 4);
    fpValueView.setUint32(0, crc, false);
    buf.set(fpValue, offset);

    return buf;
  }

  async buildNoFingerprintAsync(authKey: Uint8Array | null): Promise<Uint8Array> {
    const attrsLen = this.attrs.reduce((sum, a) => sum + a.length, 0);

    let totalLen = attrsLen;
    if (authKey) {
      totalLen += 24;
    }

    const buf = new Uint8Array(HEADER_SIZE + totalLen);
    const view = new DataView(buf.buffer, buf.byteOffset, HEADER_SIZE);

    view.setUint16(0, messageType(this.method, this.class), false);
    view.setUint32(4, MAGIC_COOKIE, false);
    buf.set(this.txID, 8);

    let offset = HEADER_SIZE;
    for (const attr of this.attrs) {
      buf.set(attr, offset);
      offset += attr.length;
    }

    if (authKey) {
      view.setUint16(2, offset - HEADER_SIZE + 24, false);

      const hashData = new Uint8Array(offset);
      hashData.set(buf.subarray(0, offset));
      const hashDataView = new DataView(hashData.buffer, hashData.byteOffset + 2, 2);
      hashDataView.setUint16(0, offset - HEADER_SIZE + 24);

      const hmac = await hmacSha1(authKey, hashData);

      const miHeader = new Uint8Array(4);
      const miHeaderView = new DataView(miHeader.buffer, miHeader.byteOffset, 4);
      miHeaderView.setUint16(0, Attr.MessageIntegrity);
      miHeaderView.setUint16(2, 20, false);
      buf.set(miHeader, offset);
      offset += 4;
      buf.set(hmac, offset);
      offset += 20;
    }

    view.setUint16(2, buf.length - HEADER_SIZE, false);

    return buf;
  }
}

async function hmacSha1(key: Uint8Array, data: Uint8Array): Promise<Uint8Array> {
  const cryptoKey = await crypto.subtle.importKey(
    "raw",
    key,
    { name: "HMAC", hash: "SHA-1" },
    false,
    ["sign"]
  );
  const signature = await crypto.subtle.sign("HMAC", cryptoKey, data);
  return new Uint8Array(signature);
}

function crc32IEEE(data: Uint8Array): number {
  let crc = 0xffffffff;
  const table = getCRC32Table();
  for (let i = 0; i < data.length; i++) {
    crc = table[(crc ^ data[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

let crc32Table: number[] | null = null;

function getCRC32Table(): number[] {
  if (crc32Table) return crc32Table;

  crc32Table = [];
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) {
      if (c & 1) {
        c = 0xedb88320 ^ (c >>> 1);
      } else {
        c = c >>> 1;
      }
    }
    crc32Table[n] = c >>> 0;
  }
  return crc32Table;
}

export function newBuilder(
  method: number,
  classVal: number,
  txID: Uint8Array
): Builder {
  return new Builder(method, classVal, txID);
}

export function newResponse(msg: Message, classVal: number): Builder {
  return new Builder(msg.method, classVal, msg.transactionID);
}

export async function checkIntegrity(data: Uint8Array, authKey: Uint8Array): Promise<Error | null> {
  if (data.length < HEADER_SIZE) {
    return new Error("message too short");
  }

  const view = new DataView(data.buffer, data.byteOffset, 20);
  const msgLen = view.getUint16(2, false);
  const end = HEADER_SIZE + msgLen;

  if (end > data.length) {
    return new Error("message length exceeds data");
  }

  let miOffset = -1;
  let offset = HEADER_SIZE;

  while (offset + 4 <= end) {
    const attrView = new DataView(data.buffer, data.byteOffset + offset, 4);
    const attrType = attrView.getUint16(0, false);
    const attrLen = attrView.getUint16(2, false);

    if (attrType === Attr.MessageIntegrity) {
      miOffset = offset;
      break;
    }

    const paddedLen = (attrLen + 3) & ~3;
    offset += 4 + paddedLen;
  }

  if (miOffset < 0) {
    return new Error("no MESSAGE-INTEGRITY attribute");
  }

  if (miOffset + 4 + 20 > data.length) {
    return new Error("MESSAGE-INTEGRITY attribute truncated");
  }

  const hashData = new Uint8Array(miOffset);
  hashData.set(data.subarray(0, miOffset));
  const hashDataView = new DataView(hashData.buffer, hashData.byteOffset + 2, 2);
  hashDataView.setUint16(0, miOffset - HEADER_SIZE + 24);

  const expectedHmac = await hmacSha1(authKey, hashData);

  const actualHmac = new Uint8Array(data.buffer, data.byteOffset + miOffset + 4, 20);

  for (let i = 0; i < 20; i++) {
    if (expectedHmac[i] !== actualHmac[i]) {
      return new Error("MESSAGE-INTEGRITY mismatch");
    }
  }

  return null;
}

export function checkFingerprint(data: Uint8Array): Error | null {
  if (data.length < HEADER_SIZE + 8) {
    return new Error("message too short for fingerprint");
  }

  const fpOffset = data.length - 8;
  const fpView = new DataView(data.buffer, data.byteOffset + fpOffset, 8);
  const attrType = fpView.getUint16(0, false);

  if (attrType !== Attr.Fingerprint) {
    return new Error(
      `last attribute is not FINGERPRINT: 0x${attrType.toString(16)}`
    );
  }

  const expected = (crc32IEEE(data.subarray(0, fpOffset)) ^ FINGERPRINT_XOR) >>> 0;
  const actual = fpView.getUint32(4, false);

  if (expected !== actual) {
    return new Error(
      `FINGERPRINT mismatch: expected 0x${expected.toString(16)}, got 0x${actual.toString(16)}`
    );
  }

  return null;
}

export function deriveAuthKey(username: string, realm: string, password: string): Uint8Array {
  const input = `${username}:${realm}:${password}`;
  return md5(new TextEncoder().encode(input));
}

function md5(data: Uint8Array): Uint8Array {
  const s = [
    7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
    5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20,
    4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
    6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21,
  ];

  const k = [
    0xd76aa478, 0xe8c7b756, 0x242070db, 0xc1bdceee,
    0xf57c0faf, 0x4787c62a, 0xa8304613, 0xfd469501,
    0x698098d8, 0x8b44f7af, 0xffff5bb1, 0x895cd7be,
    0x6b901122, 0xfd987193, 0xa679438e, 0x49b40821,
    0xf61e2562, 0xc040b340, 0x265e5a51, 0xe9b6c7aa,
    0xd62f105d, 0x02441453, 0xd8a1e681, 0xe7d3fbc8,
    0x21e1cde6, 0xc33707d6, 0xf4d50d87, 0x455a14ed,
    0xa9e3e905, 0xfcefa3f8, 0x676f02d9, 0x8d2a4c8a,
    0xfffa3942, 0x8771f681, 0x6d9d6122, 0xfde5380c,
    0xa4beea44, 0x4bdecfa9, 0xf6bb4b60, 0xbebfbc70,
    0x289b7ec6, 0xeaa127fa, 0xd4ef3085, 0x04881d05,
    0xd9d4d039, 0xe6db99e5, 0x1fa27cf8, 0xc4ac5665,
    0xf4292244, 0x432aff97, 0xab9423a7, 0xfc93a039,
    0x655b59c3, 0x8f0ccc92, 0xffeff47d, 0x85845dd1,
    0x6fa87e4f, 0xfe2ce6e0, 0xa3014314, 0x4e0811a1,
    0xf7537e82, 0xbd3af235, 0x2ad7d2bb, 0xeb86d391,
  ];

  let h0 = 0x67452301;
  let h1 = 0xefcdab89;
  let h2 = 0x98badcfe;
  let h3 = 0x10325476;

  const paddedLen = ((data.length + 8) & ~63) + 64;
  const padded = new Uint8Array(paddedLen);
  padded.set(data);
  padded[data.length] = 0x80;

  const view = new DataView(padded.buffer, padded.byteOffset, 8);
  const bitLen = data.length * 8;
  view.setUint32(4, bitLen >>> 0, false);

  for (let chunk = 0; chunk < paddedLen; chunk += 64) {
    const w = new Uint32Array(16);
    const wView = new DataView(padded.buffer, padded.byteOffset + chunk, 64);
    for (let i = 0; i < 16; i++) {
      w[i] = wView.getUint32(i * 4, false);
    }

    let a = h0;
    let b = h1;
    let c = h2;
    let d = h3;

    for (let i = 0; i < 64; i++) {
      let f: number, g: number;

      if (i < 16) {
        f = (b & c) | (~b & d);
        g = i;
      } else if (i < 32) {
        f = (d & b) | (~d & c);
        g = (5 * i + 1) % 16;
      } else if (i < 48) {
        f = b ^ c ^ d;
        g = (3 * i + 5) % 16;
      } else {
        f = c ^ (b | ~d);
        g = (7 * i) % 16;
      }

      const temp = d;
      d = c;
      c = b;
      b = (b + ((a + f + k[i] + w[g]) << s[i]) | ((a + f + k[i] + w[g]) >>> (32 - s[i]))) >>> 0;
      a = temp;
    }

    h0 = (h0 + a) >>> 0;
    h1 = (h1 + b) >>> 0;
    h2 = (h2 + c) >>> 0;
    h3 = (h3 + d) >>> 0;
  }

  const result = new Uint8Array(16);
  const resultView = new DataView(result.buffer, result.byteOffset, 16);
  resultView.setUint32(0, h0, false);
  resultView.setUint32(4, h1, false);
  resultView.setUint32(8, h2, false);
  resultView.setUint32(12, h3, false);

  return result;
}

export function ipToString(ip: Uint8Array): string {
  if (ip.length === 4) {
    return `${ip[0]}.${ip[1]}.${ip[2]}.${ip[3]}`;
  }
  if (ip.length === 16) {
    const parts: string[] = [];
    for (let i = 0; i < 16; i += 2) {
      const val = (ip[i] << 8) | ip[i + 1];
      parts.push(val.toString(16));
    }
    const full = parts.join(":");
    let bestStart = -1, bestLen = 0;
    for (let i = 0; i < parts.length;) {
      if (parts[i] !== "0") { i++; continue; }
      let j = i;
      while (j < parts.length && parts[j] === "0") j++;
      const len = j - i;
      if (len > bestLen && len >= 2) { bestStart = i; bestLen = len; }
      i = j;
    }
    if (bestStart >= 0) {
      const before = bestStart > 0 ? parts.slice(0, bestStart).join(":") : "";
      const after = bestStart + bestLen < 8 ? parts.slice(bestStart + bestLen).join(":") : "";
      if (before && after) return before + "::" + after;
      if (before) return before + "::";
      if (after) return "::" + after;
      return "::";
    }
    return full;
  }
  return "";
}

export function parseIPv4(s: string): Uint8Array | null {
  const parts = s.split(".").map(Number);
  if (parts.length !== 4 || parts.some((p) => isNaN(p) || p < 0 || p > 255)) {
    return null;
  }
  return new Uint8Array(parts);
}
