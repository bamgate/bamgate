// src/hub.ts
var Hub = class {
  peers = /* @__PURE__ */ new Map();
  peerById = /* @__PURE__ */ new Map();
  sendFn;
  constructor(sendFn) {
    this.sendFn = sendFn;
  }
  rehydrate(wsId, peerId, publicKey, address, routes, metadata) {
    const peer = {
      wsId,
      peerId,
      publicKey,
      address,
      routes,
      metadata
    };
    this.peers.set(wsId, peer);
    this.peerById.set(peerId, wsId);
  }
  join(wsId, peerId, publicKey, address, routes, metadata) {
    const peer = {
      wsId,
      peerId,
      publicKey,
      address,
      routes,
      metadata
    };
    const peerInfos = [];
    for (const existing of this.peers.values()) {
      peerInfos.push({
        peerId: existing.peerId,
        publicKey: existing.publicKey,
        address: existing.address,
        routes: existing.routes,
        metadata: existing.metadata
      });
    }
    this.peers.set(wsId, peer);
    this.peerById.set(peerId, wsId);
    return peerInfos;
  }
  message(_wsId, rawJson) {
    let msg;
    try {
      msg = JSON.parse(rawJson);
    } catch {
      return;
    }
    const type = msg.type;
    const to = "to" in msg ? msg.to : null;
    if (!to) return;
    if (type === "offer" || type === "answer" || type === "ice-candidate") {
      const targetWsId = this.peerById.get(to);
      if (targetWsId !== void 0) {
        this.sendFn(targetWsId, rawJson);
      }
    }
  }
  leave(wsId) {
    const peer = this.peers.get(wsId);
    if (!peer) return null;
    const peerId = peer.peerId;
    this.peers.delete(wsId);
    this.peerById.delete(peerId);
    return peerId;
  }
  broadcast(senderWsId, data) {
    for (const [id] of this.peers) {
      if (id === senderWsId) continue;
      this.sendFn(id, data);
    }
  }
  getPeers() {
    const peerInfos = [];
    for (const peer of this.peers.values()) {
      peerInfos.push({
        peerId: peer.peerId,
        publicKey: peer.publicKey,
        address: peer.address,
        routes: peer.routes,
        metadata: peer.metadata
      });
    }
    return peerInfos;
  }
  getPeer(wsId) {
    return this.peers.get(wsId);
  }
};
function createHub(sendFn) {
  return new Hub(sendFn);
}

// src/stun.ts
var HEADER_SIZE = 20;
var MAGIC_COOKIE = 554869826;
var FINGERPRINT_XOR = 1398035790;
var Method = {
  Binding: 1,
  Allocate: 3,
  Refresh: 4,
  Send: 6,
  Data: 7,
  CreatePermission: 8,
  ChannelBind: 9
};
var Class = {
  Request: 0,
  Indication: 1,
  SuccessResponse: 2,
  ErrorResponse: 3
};
var Attr = {
  MappedAddress: 1,
  Username: 6,
  MessageIntegrity: 8,
  ErrorCode: 9,
  ChannelNumber: 12,
  Lifetime: 13,
  XORPeerAddress: 18,
  Data: 19,
  Realm: 20,
  Nonce: 21,
  XORRelayedAddress: 22,
  RequestedTransport: 25,
  XORMappedAddress: 32,
  Fingerprint: 32808,
  Software: 32802
};
var FamilyIPv4 = 1;
var FamilyIPv6 = 2;
function messageType(method, classVal) {
  const m = method & 4095;
  const c = classVal & 3;
  return m & 15 | (c & 1) << 4 | (m & 112) << 1 | (c & 2) << 7 | (m & 3968) << 2;
}
function parseType(t) {
  const method = t & 15 | t >> 1 & 112 | t >> 2 & 3968;
  const classVal = t >> 4 & 1 | t >> 7 & 2;
  return { method, class: classVal };
}
function isChannelData(data) {
  if (data.length < 4) return false;
  const view = new DataView(data.buffer, data.byteOffset, 2);
  const ch = view.getUint16(0, false);
  return ch >= 16384 && ch <= 32767;
}
function isSTUN(data) {
  if (data.length < HEADER_SIZE) return false;
  if (data[0] & 192) return false;
  const view = new DataView(data.buffer, data.byteOffset + 4, 4);
  const cookie = view.getUint32(0, false);
  return cookie === MAGIC_COOKIE;
}
function parseChannelData(data) {
  const view = new DataView(data.buffer, data.byteOffset, 4);
  const channelNumber = view.getUint16(0, false);
  const length = view.getUint16(2, false);
  const payload = new Uint8Array(data.buffer, data.byteOffset + 4, length);
  return { channelNumber, data: payload };
}
function buildChannelData(channelNumber, payload) {
  const paddedLen = payload.length + 3 & ~3;
  const buf = new Uint8Array(4 + paddedLen);
  const view = new DataView(buf.buffer, buf.byteOffset, 4);
  view.setUint16(0, channelNumber, false);
  view.setUint16(2, payload.length, false);
  buf.set(payload, 4);
  return buf;
}
function parse(data) {
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
  const attributes = [];
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
    const paddedLen = attrLen + 3 & ~3;
    offset += 4 + paddedLen;
  }
  return { method, class: classVal, transactionID, attributes };
}
function getAttr(msg, attrType) {
  for (const attr of msg.attributes) {
    if (attr.type === attrType) {
      return attr.value;
    }
  }
  return null;
}
function getAttrs(msg, attrType) {
  const result = [];
  for (const attr of msg.attributes) {
    if (attr.type === attrType) {
      result.push(attr.value);
    }
  }
  return result;
}
function getUsername(msg) {
  const v = getAttr(msg, Attr.Username);
  return v ? new TextDecoder().decode(v) : "";
}
function getLifetime(msg) {
  const v = getAttr(msg, Attr.Lifetime);
  if (!v || v.length < 4) return 0;
  const view = new DataView(v.buffer, v.byteOffset, 4);
  return view.getUint32(0, false);
}
function getChannelNumber(msg) {
  const v = getAttr(msg, Attr.ChannelNumber);
  if (!v || v.length < 2) return 0;
  const view = new DataView(v.buffer, v.byteOffset, 2);
  return view.getUint16(0, false);
}
function getData(msg) {
  return getAttr(msg, Attr.Data);
}
function decodeXORAddress(value, txID) {
  if (value.length < 4) {
    return { ip: new Uint8Array(0), port: 0 };
  }
  const family = value[1];
  const view = new DataView(value.buffer, value.byteOffset + 2, 2);
  const xorPort = view.getUint16(0, false);
  const port = xorPort ^ MAGIC_COOKIE >> 16;
  let ip;
  if (family === FamilyIPv4) {
    if (value.length < 8) {
      return { ip: new Uint8Array(0), port: 0 };
    }
    ip = new Uint8Array(4);
    for (let i = 0; i < 4; i++) {
      ip[i] = value[4 + i] ^ MAGIC_COOKIE >> 24 - i * 8 & 255;
    }
  } else if (family === FamilyIPv6) {
    if (value.length < 20) {
      return { ip: new Uint8Array(0), port: 0 };
    }
    ip = new Uint8Array(16);
    for (let i = 0; i < 4; i++) {
      ip[i] = value[4 + i] ^ MAGIC_COOKIE >> 24 - i * 8 & 255;
    }
    for (let i = 0; i < 12; i++) {
      ip[4 + i] = value[8 + i] ^ txID[i];
    }
  } else {
    return { ip: new Uint8Array(0), port: 0 };
  }
  return { ip, port };
}
function getXORPeerAddress(msg) {
  const v = getAttr(msg, Attr.XORPeerAddress);
  return v ? decodeXORAddress(v, msg.transactionID) : null;
}
function getXORPeerAddresses(msg) {
  const vals = getAttrs(msg, Attr.XORPeerAddress);
  return vals.map((v) => decodeXORAddress(v, msg.transactionID));
}
var Builder = class {
  method;
  class;
  txID;
  attrs = [];
  constructor(method, classVal, txID) {
    this.method = method;
    this.class = classVal;
    this.txID = txID;
  }
  addRaw(attrType, value) {
    const header = new Uint8Array(4);
    const view = new DataView(header.buffer, header.byteOffset, 4);
    view.setUint16(0, attrType, false);
    view.setUint16(2, value.length, false);
    this.attrs.push(header);
    this.attrs.push(value);
    const pad = (4 - value.length % 4) % 4;
    if (pad > 0) {
      this.attrs.push(new Uint8Array(pad));
    }
    return this;
  }
  addString(attrType, s) {
    return this.addRaw(attrType, new TextEncoder().encode(s));
  }
  addUsername(username) {
    return this.addString(Attr.Username, username);
  }
  addRealm(realm) {
    return this.addString(Attr.Realm, realm);
  }
  addNonce(nonce) {
    return this.addString(Attr.Nonce, nonce);
  }
  addLifetime(seconds) {
    const v = new Uint8Array(4);
    const view = new DataView(v.buffer, v.byteOffset, 4);
    view.setUint32(0, seconds, false);
    return this.addRaw(Attr.Lifetime, v);
  }
  addErrorCode(code, reason) {
    const value = new Uint8Array(4 + new TextEncoder().encode(reason).length);
    const view = new DataView(value.buffer, value.byteOffset, 4);
    view.setUint8(2, Math.floor(code / 100));
    view.setUint8(3, code % 100);
    const reasonBytes = new TextEncoder().encode(reason);
    value.set(reasonBytes, 4);
    return this.addRaw(Attr.ErrorCode, value);
  }
  addXORAddress(attrType, addr) {
    const ip4 = addr.ip.length === 4 ? addr.ip : null;
    const ip6 = addr.ip.length === 16 ? addr.ip : null;
    if (ip4) {
      const value = new Uint8Array(8);
      value[1] = FamilyIPv4;
      const view = new DataView(value.buffer, value.byteOffset + 2, 2);
      view.setUint16(0, addr.port ^ MAGIC_COOKIE >> 16, false);
      for (let i = 0; i < 4; i++) {
        value[4 + i] = ip4[i] ^ MAGIC_COOKIE >> 24 - i * 8 & 255;
      }
      return this.addRaw(attrType, value);
    }
    if (ip6) {
      const value = new Uint8Array(20);
      value[1] = FamilyIPv6;
      const view = new DataView(value.buffer, value.byteOffset + 2, 2);
      view.setUint16(0, addr.port ^ MAGIC_COOKIE >> 16, false);
      for (let i = 0; i < 4; i++) {
        value[4 + i] = ip6[i] ^ MAGIC_COOKIE >> 24 - i * 8 & 255;
      }
      for (let i = 0; i < 12; i++) {
        value[8 + i] = ip6[4 + i] ^ this.txID[i];
      }
      return this.addRaw(attrType, value);
    }
    return this;
  }
  addData(data) {
    return this.addRaw(Attr.Data, data);
  }
  addChannelNumber(ch) {
    const v = new Uint8Array(4);
    const view = new DataView(v.buffer, v.byteOffset, 4);
    view.setUint16(0, ch, false);
    return this.addRaw(Attr.ChannelNumber, v);
  }
  async buildAsync(authKey) {
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
  async buildNoFingerprintAsync(authKey) {
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
};
async function hmacSha1(key, data) {
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
function crc32IEEE(data) {
  let crc = 4294967295;
  const table = getCRC32Table();
  for (let i = 0; i < data.length; i++) {
    crc = table[(crc ^ data[i]) & 255] ^ crc >>> 8;
  }
  return (crc ^ 4294967295) >>> 0;
}
var crc32Table = null;
function getCRC32Table() {
  if (crc32Table) return crc32Table;
  crc32Table = [];
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) {
      if (c & 1) {
        c = 3988292384 ^ c >>> 1;
      } else {
        c = c >>> 1;
      }
    }
    crc32Table[n] = c >>> 0;
  }
  return crc32Table;
}
function newBuilder(method, classVal, txID) {
  return new Builder(method, classVal, txID);
}
function newResponse(msg, classVal) {
  return new Builder(msg.method, classVal, msg.transactionID);
}
async function checkIntegrity(data, authKey) {
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
    const paddedLen = attrLen + 3 & ~3;
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
function deriveAuthKey(username, realm, password) {
  const input = `${username}:${realm}:${password}`;
  return md5(new TextEncoder().encode(input));
}
function md5(data) {
  const s = [
    7,
    12,
    17,
    22,
    7,
    12,
    17,
    22,
    7,
    12,
    17,
    22,
    7,
    12,
    17,
    22,
    5,
    9,
    14,
    20,
    5,
    9,
    14,
    20,
    5,
    9,
    14,
    20,
    5,
    9,
    14,
    20,
    4,
    11,
    16,
    23,
    4,
    11,
    16,
    23,
    4,
    11,
    16,
    23,
    4,
    11,
    16,
    23,
    6,
    10,
    15,
    21,
    6,
    10,
    15,
    21,
    6,
    10,
    15,
    21,
    6,
    10,
    15,
    21
  ];
  const k = [
    3614090360,
    3905402710,
    606105819,
    3250441966,
    4118548399,
    1200080426,
    2821735955,
    4249261313,
    1770035416,
    2336552879,
    4294925233,
    2304563134,
    1804603682,
    4254626195,
    2792965006,
    1236535329,
    4129170786,
    3225465664,
    643717713,
    3921069994,
    3593408605,
    38016083,
    3634488961,
    3889429448,
    568446438,
    3275163606,
    4107603335,
    1163531501,
    2850285829,
    4243563512,
    1735328473,
    2368359562,
    4294588738,
    2272392833,
    1839030562,
    4259657740,
    2763975236,
    1272893353,
    4139469664,
    3200236656,
    681279174,
    3936430074,
    3572445317,
    76029189,
    3654602809,
    3873151461,
    530742520,
    3299628645,
    4096336452,
    1126891415,
    2878612391,
    4237533241,
    1700485571,
    2399980690,
    4293915773,
    2240044497,
    1873313359,
    4264355552,
    2734768916,
    1309151649,
    4149444226,
    3174756917,
    718787259,
    3951481745
  ];
  let h0 = 1732584193;
  let h1 = 4023233417;
  let h2 = 2562383102;
  let h3 = 271733878;
  const paddedLen = (data.length + 8 & ~63) + 64;
  const padded = new Uint8Array(paddedLen);
  padded.set(data);
  padded[data.length] = 128;
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
      let f, g;
      if (i < 16) {
        f = b & c | ~b & d;
        g = i;
      } else if (i < 32) {
        f = d & b | ~d & c;
        g = (5 * i + 1) % 16;
      } else if (i < 48) {
        f = b ^ c ^ d;
        g = (3 * i + 5) % 16;
      } else {
        f = c ^ (b | ~d);
        g = 7 * i % 16;
      }
      const temp = d;
      d = c;
      c = b;
      b = (b + (a + f + k[i] + w[g] << s[i]) | a + f + k[i] + w[g] >>> 32 - s[i]) >>> 0;
      a = temp;
    }
    h0 = h0 + a >>> 0;
    h1 = h1 + b >>> 0;
    h2 = h2 + c >>> 0;
    h3 = h3 + d >>> 0;
  }
  const result = new Uint8Array(16);
  const resultView = new DataView(result.buffer, result.byteOffset, 16);
  resultView.setUint32(0, h0, false);
  resultView.setUint32(4, h1, false);
  resultView.setUint32(8, h2, false);
  resultView.setUint32(12, h3, false);
  return result;
}
function ipToString(ip) {
  if (ip.length === 4) {
    return `${ip[0]}.${ip[1]}.${ip[2]}.${ip[3]}`;
  }
  if (ip.length === 16) {
    const parts = [];
    for (let i = 0; i < 16; i += 2) {
      const val = ip[i] << 8 | ip[i + 1];
      parts.push(val.toString(16));
    }
    const full = parts.join(":");
    let bestStart = -1, bestLen = 0;
    for (let i = 0; i < parts.length; ) {
      if (parts[i] !== "0") {
        i++;
        continue;
      }
      let j = i;
      while (j < parts.length && parts[j] === "0") j++;
      const len = j - i;
      if (len > bestLen && len >= 2) {
        bestStart = i;
        bestLen = len;
      }
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
function parseIPv4(s) {
  const parts = s.split(".").map(Number);
  if (parts.length !== 4 || parts.some((p) => isNaN(p) || p < 0 || p > 255)) {
    return null;
  }
  return new Uint8Array(parts);
}

// src/turn.ts
var TURN_REALM = "bamgate";
var DEFAULT_ALLOC_LIFETIME = 600;
var MAX_ALLOC_LIFETIME = 3600;
var RELAY_BASE_IP = "10.255.0.0";
var RELAY_BASE_PORT = 5e4;
var MAX_RELAY_HOST = 255;
var TurnServer = class {
  allocations = /* @__PURE__ */ new Map();
  relayByAddr = /* @__PURE__ */ new Map();
  nextRelayHost = 1;
  sendBinaryFn;
  getTURNSecretFn;
  saveTURNAllocFn;
  constructor(sendBinaryFn, getTURNSecretFn, saveTURNAllocFn) {
    this.sendBinaryFn = sendBinaryFn;
    this.getTURNSecretFn = getTURNSecretFn;
    this.saveTURNAllocFn = saveTURNAllocFn;
  }
  generateNonce() {
    return String(Date.now() * 1e6 + Math.floor(Math.random() * 1e6));
  }
  getTURNSecret() {
    return this.getTURNSecretFn();
  }
  validateTURNCredentials(username, password) {
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
    if (Math.floor(Date.now() / 1e3) > expiry) {
      return new Error("credentials expired");
    }
    const mac = this.computeHMACSHA1(secret, username);
    const expected = this.base64Encode(mac);
    if (password !== expected) {
      return new Error("invalid password");
    }
    return null;
  }
  computeHMACSHA1(key, data) {
    const cryptoKey = new TextEncoder().encode(key);
    const msgData = new TextEncoder().encode(data);
    const ipad = new Uint8Array(64);
    const opad = new Uint8Array(64);
    for (let i = 0; i < 64; i++) {
      ipad[i] = i < cryptoKey.length ? cryptoKey[i] ^ 54 : 54;
      opad[i] = i < cryptoKey.length ? cryptoKey[i] ^ 92 : 92;
    }
    const innerData = new Uint8Array(64 + msgData.length);
    innerData.set(ipad, 0);
    innerData.set(msgData, 64);
    return this.sha1(innerData);
  }
  sha1(data) {
    const h = [1732584193, 4023233417, 2562383102, 271733878];
    const padded = new Uint8Array((data.length + 8 & ~63) + 64);
    padded.set(data);
    padded[data.length] = 128;
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
        w[i] = v << 1 | v >>> 31;
      }
      let a = h[0], b = h[1], c = h[2], d = h[3];
      for (let i = 0; i < 80; i++) {
        let f, k;
        if (i < 20) {
          f = b & c | ~b & d;
          k = 1518500249;
        } else if (i < 40) {
          f = b ^ c ^ d;
          k = 1859775393;
        } else if (i < 60) {
          f = b & c | b & d | c & d;
          k = 2400959708;
        } else {
          f = b ^ c ^ d;
          k = 3395469782;
        }
        const temp = (a << 5 | a >>> 27) + f + k + w[i];
        a = temp >>> 0;
        const temp2 = d;
        d = c;
        c = b << 30 | b >>> 2;
        b = a;
        a = temp2;
      }
      h[0] = h[0] + a >>> 0;
      h[1] = h[1] + b >>> 0;
      h[2] = h[2] + c >>> 0;
      h[3] = h[3] + d >>> 0;
    }
    const result = new Uint8Array(20);
    const resultView = new DataView(result.buffer, result.byteOffset, 20);
    resultView.setUint32(0, h[0], false);
    resultView.setUint32(4, h[1], false);
    resultView.setUint32(8, h[2], false);
    resultView.setUint32(12, h[3], false);
    return result;
  }
  base64Encode(data) {
    let binary = "";
    for (let i = 0; i < data.length; i++) {
      binary += String.fromCharCode(data[i]);
    }
    return btoa(binary);
  }
  recomputePassword(username) {
    const secret = this.getTURNSecret();
    const mac = this.computeHMACSHA1(secret, username);
    return this.base64Encode(mac);
  }
  deriveAuthKey(username, realm, password) {
    return deriveAuthKey(username, realm, password);
  }
  assignRelayAddr() {
    if (this.nextRelayHost > MAX_RELAY_HOST) {
      return null;
    }
    const ip = parseIPv4(RELAY_BASE_IP);
    if (!ip) return null;
    const relayIp = new Uint8Array(4);
    relayIp[0] = ip[0];
    relayIp[1] = ip[1];
    relayIp[2] = ip[2];
    relayIp[3] = this.nextRelayHost;
    this.nextRelayHost++;
    return { ip: relayIp, port: RELAY_BASE_PORT };
  }
  addrKey(addr) {
    return `${ipToString(addr.ip)}:${addr.port}`;
  }
  handleMessage(wsId, data) {
    if (isChannelData(data)) {
      this.handleChannelData(wsId, data);
      return;
    }
    if (!isSTUN(data)) {
      return;
    }
    let msg;
    try {
      msg = parse(data);
    } catch {
      return;
    }
    switch (msg.method) {
      case Method.Binding:
        this.handleBinding(wsId, msg);
        break;
      case Method.Allocate:
        this.handleAllocate(wsId, msg, data);
        break;
      case Method.Refresh:
        this.handleRefresh(wsId, msg, data);
        break;
      case Method.CreatePermission:
        this.handleCreatePermission(wsId, msg, data);
        break;
      case Method.ChannelBind:
        this.handleChannelBind(wsId, msg, data);
        break;
      case Method.Send:
        this.handleSend(wsId, msg);
        break;
    }
  }
  async handleBinding(wsId, msg) {
    const relayAddr = { ip: parseIPv4("127.0.0.1"), port: 1234 };
    const response = newResponse(msg, Class.SuccessResponse).addXORAddress(Attr.XORMappedAddress, relayAddr).buildAsync(null);
    this.sendBinaryFn(wsId, await response);
  }
  async handleAllocate(wsId, msg, rawData) {
    const existing = this.allocations.get(wsId);
    const username = getUsername(msg);
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
          permissions: /* @__PURE__ */ new Set(),
          channels: /* @__PURE__ */ new Map(),
          channelByAddr: /* @__PURE__ */ new Map()
        });
      } else {
        existing.nonce = nonce;
      }
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(401, "Unauthorized").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
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
        permissions: /* @__PURE__ */ new Set(),
        channels: /* @__PURE__ */ new Map(),
        channelByAddr: /* @__PURE__ */ new Map()
      });
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(401, "Unauthorized").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const password = this.recomputePassword(username);
    const credError = this.validateTURNCredentials(username, password);
    if (credError) {
      const nonce = this.generateNonce();
      existing.nonce = nonce;
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(401, "Unauthorized").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const authKey = this.deriveAuthKey(username, TURN_REALM, password);
    const integrityError = await checkIntegrity(rawData, authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      existing.nonce = nonce;
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(401, "Unauthorized").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    if (existing.relayAddr.ip.length > 0) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(437, "Allocation Mismatch").buildAsync(authKey);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const relayAddr = this.assignRelayAddr();
    if (!relayAddr) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(508, "Insufficient Capacity").buildAsync(authKey);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const lifetime = DEFAULT_ALLOC_LIFETIME;
    existing.username = username;
    existing.authKey = authKey;
    existing.relayAddr = relayAddr;
    existing.createdAt = Math.floor(Date.now() / 1e3);
    existing.lifetime = lifetime;
    this.relayByAddr.set(this.addrKey(relayAddr), wsId);
    const mappedAddr = { ip: parseIPv4("127.0.0.1"), port: 1234 };
    const response = newResponse(msg, Class.SuccessResponse).addXORAddress(Attr.XORRelayedAddress, relayAddr).addXORAddress(Attr.XORMappedAddress, mappedAddr).addLifetime(lifetime).buildAsync(authKey);
    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }
  async handleRefresh(wsId, msg, rawData) {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(437, "Allocation Mismatch").buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const integrityError = await checkIntegrity(rawData, alloc.authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      alloc.nonce = nonce;
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(438, "Stale Nonce").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const requestedLifetime = getLifetime(msg);
    if (requestedLifetime === 0) {
      const response2 = newResponse(msg, Class.SuccessResponse).addLifetime(0).buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response2);
      this.removeAllocation(wsId);
      return;
    }
    const lifetime = Math.min(requestedLifetime, MAX_ALLOC_LIFETIME);
    alloc.lifetime = lifetime;
    const response = newResponse(msg, Class.SuccessResponse).addLifetime(lifetime).buildAsync(alloc.authKey);
    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }
  async handleCreatePermission(wsId, msg, rawData) {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(437, "Allocation Mismatch").buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const integrityError = await checkIntegrity(rawData, alloc.authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      alloc.nonce = nonce;
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(438, "Stale Nonce").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const addrs = getXORPeerAddresses(msg);
    for (const addr of addrs) {
      alloc.permissions.add(ipToString(addr.ip));
    }
    const response = newResponse(msg, Class.SuccessResponse).buildAsync(alloc.authKey);
    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }
  async handleChannelBind(wsId, msg, rawData) {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(437, "Allocation Mismatch").buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const integrityError = await checkIntegrity(rawData, alloc.authKey);
    if (integrityError) {
      const nonce = this.generateNonce();
      alloc.nonce = nonce;
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(438, "Stale Nonce").addRealm(TURN_REALM).addNonce(nonce).buildAsync(null);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const channelNumber = getChannelNumber(msg);
    if (channelNumber < 16384 || channelNumber > 32767) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(400, "Bad Request").buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const peerAddr = getXORPeerAddress(msg);
    if (!peerAddr || peerAddr.ip.length === 0) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(400, "Bad Request").buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    const peerKey = this.addrKey(peerAddr);
    const existingAddr = alloc.channels.get(channelNumber);
    if (existingAddr !== void 0 && existingAddr !== peerKey) {
      const response2 = newResponse(msg, Class.ErrorResponse).addErrorCode(400, "Bad Request").buildAsync(alloc.authKey);
      this.sendBinaryFn(wsId, await response2);
      return;
    }
    alloc.channels.set(channelNumber, peerKey);
    alloc.channelByAddr.set(peerKey, channelNumber);
    alloc.permissions.add(ipToString(peerAddr.ip));
    const response = newResponse(msg, Class.SuccessResponse).buildAsync(alloc.authKey);
    this.sendBinaryFn(wsId, await response);
    this.saveAllocation(wsId);
  }
  async handleSend(wsId, msg) {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      return;
    }
    const peerAddr = getXORPeerAddress(msg);
    if (!peerAddr || peerAddr.ip.length === 0) {
      return;
    }
    const data = getData(msg);
    if (!data) {
      return;
    }
    const peerIpStr = ipToString(peerAddr.ip);
    if (!alloc.permissions.has(peerIpStr)) {
      return;
    }
    const targetKey = this.addrKey(peerAddr);
    const targetWsId = this.relayByAddr.get(targetKey);
    if (targetWsId === void 0) {
      return;
    }
    const targetAlloc = this.allocations.get(targetWsId);
    if (!targetAlloc) {
      return;
    }
    const ourKey = this.addrKey(alloc.relayAddr);
    const channelNum = targetAlloc.channelByAddr.get(ourKey);
    if (channelNum !== void 0) {
      const frame = buildChannelData(channelNum, data);
      this.sendBinaryFn(targetWsId, frame);
      return;
    }
    const txID = new Uint8Array(12);
    const ind = newBuilder(Method.Data, Class.Indication, txID).addXORAddress(Attr.XORPeerAddress, alloc.relayAddr).addData(data).buildNoFingerprintAsync(null);
    this.sendBinaryFn(targetWsId, await ind);
  }
  async handleChannelData(wsId, data) {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0) {
      return;
    }
    const cd = parseChannelData(data);
    const peerKey = alloc.channels.get(cd.channelNumber);
    if (!peerKey) {
      return;
    }
    const targetWsId = this.relayByAddr.get(peerKey);
    if (targetWsId === void 0) {
      return;
    }
    const targetAlloc = this.allocations.get(targetWsId);
    if (!targetAlloc) {
      return;
    }
    const ourKey = this.addrKey(alloc.relayAddr);
    const channelNum = targetAlloc.channelByAddr.get(ourKey);
    if (channelNum !== void 0) {
      const frame = buildChannelData(channelNum, cd.data);
      this.sendBinaryFn(targetWsId, frame);
      return;
    }
    const txID = new Uint8Array(12);
    const ind = newBuilder(Method.Data, Class.Indication, txID).addXORAddress(Attr.XORPeerAddress, alloc.relayAddr).addData(cd.data).buildNoFingerprintAsync(null);
    this.sendBinaryFn(targetWsId, await ind);
  }
  close(wsId) {
    this.removeAllocation(wsId);
  }
  removeAllocation(wsId) {
    const alloc = this.allocations.get(wsId);
    if (!alloc) return;
    if (alloc.relayAddr.ip.length > 0) {
      this.relayByAddr.delete(this.addrKey(alloc.relayAddr));
    }
    this.allocations.delete(wsId);
  }
  rehydrate(wsId, jsonStr) {
    const j = JSON.parse(jsonStr);
    const authKey = this.base64Decode(j.ak);
    const relayIp = parseIPv4(j.ri);
    if (!relayIp) return;
    const relayAddr = { ip: relayIp, port: j.rp };
    const permissions = /* @__PURE__ */ new Set();
    if (j.pm) {
      for (const ip of j.pm) {
        permissions.add(ip);
      }
    }
    const channels = /* @__PURE__ */ new Map();
    const channelByAddr = /* @__PURE__ */ new Map();
    if (j.ch) {
      for (const [numStr, addr] of Object.entries(j.ch)) {
        const num = parseInt(numStr, 10);
        channels.set(num, addr);
        channelByAddr.set(addr, num);
      }
    }
    const alloc = {
      wsId,
      username: j.u,
      nonce: j.n,
      authKey,
      relayAddr,
      createdAt: j.ca,
      lifetime: j.lt,
      permissions,
      channels,
      channelByAddr
    };
    this.allocations.set(wsId, alloc);
    this.relayByAddr.set(this.addrKey(relayAddr), wsId);
    if (j.rh >= this.nextRelayHost) {
      this.nextRelayHost = j.rh + 1;
    }
  }
  base64Decode(str) {
    while (str.length % 4 !== 0) str += "=";
    const binary = atob(str.replace(/-/g, "+").replace(/_/g, "/"));
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  }
  saveAllocation(wsId) {
    const alloc = this.allocations.get(wsId);
    if (!alloc || alloc.authKey.length === 0 || alloc.relayAddr.ip.length === 0) {
      return;
    }
    const perms = [];
    for (const ip of alloc.permissions) {
      perms.push(ip);
    }
    const channels = {};
    for (const [num, addr] of alloc.channels) {
      channels[String(num)] = addr;
    }
    const j = {
      u: alloc.username,
      n: alloc.nonce,
      ak: this.base64Encode(alloc.authKey),
      ri: ipToString(alloc.relayAddr.ip),
      rp: alloc.relayAddr.port,
      rh: alloc.relayAddr.ip[3],
      ca: alloc.createdAt,
      lt: alloc.lifetime,
      pm: perms,
      ch: channels
    };
    this.saveTURNAllocFn(wsId, JSON.stringify(j));
  }
};
function createTurnServer(sendBinaryFn, getTURNSecretFn, saveTURNAllocFn) {
  return new TurnServer(sendBinaryFn, getTURNSecretFn, saveTURNAllocFn);
}

// src/auth.ts
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
  return Array.from(bytes).map((b) => b.toString(16).padStart(2, "0")).join("");
}
function hexDecode(hex) {
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substring(i, i + 2), 16);
  }
  return bytes;
}
var Auth = class {
  keyCache = /* @__PURE__ */ new Map();
  async importHMACKey(secretHex) {
    if (this.keyCache.has(secretHex)) {
      return this.keyCache.get(secretHex);
    }
    const keyData = hexDecode(secretHex);
    const key = await crypto.subtle.importKey(
      "raw",
      keyData,
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"]
    );
    this.keyCache.set(secretHex, key);
    return key;
  }
  async signJWT(payload, getSigningKey) {
    const { kid, secretHex } = await getSigningKey();
    const key = await this.importHMACKey(secretHex);
    const header = { alg: "HS256", typ: "JWT", kid };
    const encodedHeader = base64urlEncode(JSON.stringify(header));
    const encodedPayload = base64urlEncode(JSON.stringify(payload));
    const signingInput = encodedHeader + "." + encodedPayload;
    const signature = await crypto.subtle.sign(
      "HMAC",
      key,
      new TextEncoder().encode(signingInput)
    );
    const encodedSignature = base64urlEncode(new Uint8Array(signature));
    return signingInput + "." + encodedSignature;
  }
  async verifyJWT(token, getSigningKey) {
    const parts = token.split(".");
    if (parts.length !== 3) return null;
    let header;
    try {
      header = JSON.parse(new TextDecoder().decode(base64urlDecode(parts[0])));
    } catch {
      return null;
    }
    if (header.alg !== "HS256" || !header.kid) return null;
    const signingKey = await getSigningKey(header.kid);
    if (!signingKey) return null;
    const key = await this.importHMACKey(signingKey.secretHex);
    const signingInput = new TextEncoder().encode(parts[0] + "." + parts[1]);
    const signature = base64urlDecode(parts[2]);
    const valid = await crypto.subtle.verify("HMAC", key, signature, signingInput);
    if (!valid) return null;
    let payload;
    try {
      payload = JSON.parse(new TextDecoder().decode(base64urlDecode(parts[1])));
    } catch {
      return null;
    }
    const now = Math.floor(Date.now() / 1e3);
    if (payload.exp && payload.exp < now) return null;
    return payload;
  }
  generateRefreshToken() {
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    return "bgr_" + hexEncode(bytes);
  }
  async hashToken(token) {
    const data = new TextEncoder().encode(token);
    const hash = await crypto.subtle.digest("SHA-256", data);
    return hexEncode(new Uint8Array(hash));
  }
  async verifyGitHubToken(githubToken) {
    const resp = await fetch("https://api.github.com/user", {
      headers: {
        Authorization: `Bearer ${githubToken}`,
        "User-Agent": "bamgate-worker",
        Accept: "application/vnd.github+json"
      }
    });
    if (!resp.ok) return null;
    const user = await resp.json();
    return { id: String(user.id), login: user.login };
  }
};
var auth = new Auth();

// src/worker.ts
var worker_default = {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname === "/status") {
      return new Response(JSON.stringify({ status: "ok" }), {
        headers: { "content-type": "application/json" }
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
          "Content-Type": "application/json"
        },
        body: request.body
      });
      return stub.fetch(doReq);
    }
    if (url.pathname === "/auth/refresh" && request.method === "POST") {
      const doReq = new Request(request.url, {
        method: "POST",
        headers: {
          "X-Bamgate-Action": "auth-refresh",
          "Content-Type": "application/json"
        },
        body: request.body
      });
      return stub.fetch(doReq);
    }
    const authHeader = request.headers.get("Authorization");
    if (!authHeader || !authHeader.startsWith("Bearer ")) {
      return new Response(JSON.stringify({ error: "missing authorization" }), {
        status: 401,
        headers: { "content-type": "application/json" }
      });
    }
    const token = authHeader.slice(7);
    const authHeaders = [["X-Bamgate-JWT", token]];
    if (url.pathname === "/connect") {
      const headers = new Headers([...request.headers.entries(), ...authHeaders]);
      const doReq = new Request(request.url, { method: request.method, headers });
      return stub.fetch(doReq);
    }
    if (url.pathname === "/turn") {
      const headers = new Headers([
        ...request.headers.entries(),
        ...authHeaders,
        ["X-Bamgate-Turn", "1"]
      ]);
      const doReq = new Request(request.url, { method: request.method, headers });
      return stub.fetch(doReq);
    }
    if (url.pathname === "/auth/devices" && request.method === "GET") {
      const doReq = new Request(request.url, {
        method: "GET",
        headers: { "X-Bamgate-Action": "list-devices", "X-Bamgate-JWT": token }
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
          "X-Bamgate-Device-ID": revokeMatch[1]
        }
      });
      return stub.fetch(doReq);
    }
    return new Response("Not Found", { status: 404 });
  }
};
var SignalingRoom = class {
  ctx;
  nextWsId = 1;
  wsMap = /* @__PURE__ */ new Map();
  ready = false;
  readyPromise = null;
  hub;
  turnServer;
  constructor(ctx, _env) {
    this.ctx = ctx;
    this.ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
    const sendJson = (wsId, data) => {
      const ws = this.wsMap.get(wsId);
      if (ws) {
        try {
          ws.send(data);
        } catch {
        }
      }
    };
    const sendBinary = (wsId, data) => {
      const ws = this.wsMap.get(wsId);
      if (ws) {
        try {
          const buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);
          ws.send(buf);
        } catch {
        }
      }
    };
    const getTurnSecret = () => {
      return this._getOrCreateTURNSecret();
    };
    const saveTurnAlloc = (wsId, json) => {
      const ws = this.wsMap.get(wsId);
      if (ws) {
        const attachment = ws.deserializeAttachment();
        attachment.turnAlloc = JSON.parse(json);
        ws.serializeAttachment(attachment);
      }
    };
    this.hub = createHub(sendJson);
    this.turnServer = createTurnServer(sendBinary, getTurnSecret, saveTurnAlloc);
  }
  async fetch(request) {
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
    const [client, server] = Object.values(pair);
    const wsId = this.nextWsId++;
    const isTurn = request.headers.get("X-Bamgate-Turn") === "1";
    this.wsMap.set(wsId, server);
    this.ctx.acceptWebSocket(server);
    server.serializeAttachment({
      wsId,
      joined: false,
      isTurn,
      deviceId: claims.sub
    });
    return new Response(null, { status: 101, webSocket: client });
  }
  async webSocketMessage(ws, message) {
    await this.ensureReady();
    const attachment = ws.deserializeAttachment();
    if (!attachment) return;
    const wsId = attachment.wsId;
    if (attachment.isTurn) {
      if (typeof message === "string") return;
      const data = message instanceof ArrayBuffer ? new Uint8Array(message) : new Uint8Array(message);
      this.turnServer.handleMessage(wsId, data);
      return;
    }
    if (!attachment.joined) {
      let msg;
      try {
        msg = JSON.parse(typeof message === "string" ? message : new TextDecoder().decode(message));
      } catch {
        return;
      }
      if (msg.type !== "join" || !msg.peerId) return;
      const newAttachment = {
        wsId,
        joined: true,
        isTurn: false,
        deviceId: attachment.deviceId,
        peerId: msg.peerId,
        publicKey: msg.publicKey || "",
        address: msg.address || "",
        routes: msg.routes || [],
        metadata: msg.metadata || {}
      };
      ws.serializeAttachment(newAttachment);
      if (attachment.deviceId) {
        this._ensureTables();
        const now = Math.floor(Date.now() / 1e3);
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
      const joinWs = this.wsMap.get(wsId);
      if (joinWs) {
        try {
          joinWs.send(peersMsg);
        } catch {
        }
      }
      const joinedMsg = JSON.stringify({
        type: "peer-joined",
        peerId: msg.peerId,
        publicKey: msg.publicKey || "",
        address: msg.address || "",
        routes: msg.routes || [],
        metadata: msg.metadata || {}
      });
      this.hub.broadcast(wsId, joinedMsg);
      return;
    }
    const text = typeof message === "string" ? message : new TextDecoder().decode(message);
    this.hub.message(wsId, text);
  }
  async webSocketClose(ws, code, reason, _wasClean) {
    await this.ensureReady();
    const attachment = ws.deserializeAttachment();
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
    }
  }
  async webSocketError(ws, _error) {
    await this.ensureReady();
    const attachment = ws.deserializeAttachment();
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
    }
  }
  async ensureReady() {
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
  _rehydrate() {
    const sockets = this.ctx.getWebSockets();
    let maxWsId = this.nextWsId;
    for (const ws of sockets) {
      const attachment = ws.deserializeAttachment();
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
        prev_refresh_token_hash TEXT,
        refresh_token_expires_at INTEGER NOT NULL,
        revoked INTEGER DEFAULT 0,
        created_at INTEGER NOT NULL,
        last_seen_at INTEGER
      )
    `);
    try {
      this.ctx.storage.sql.exec(`ALTER TABLE devices ADD COLUMN prev_refresh_token_hash TEXT`);
    } catch {
    }
    this.ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS network (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      )
    `);
    this._tablesReady = true;
  }
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
    const bytes = new Uint8Array(24);
    crypto.getRandomValues(bytes);
    const hex = Array.from(bytes).map((b) => b.toString(16).padStart(2, "0")).join("");
    const finalSecret = "bg_" + hex;
    this.ctx.storage.sql.exec("INSERT INTO network (key, value) VALUES ('turn_secret', ?)", finalSecret);
    return finalSecret;
  }
  _getAssignedAddresses() {
    this._ensureTables();
    const rows = [...this.ctx.storage.sql.exec("SELECT address FROM devices WHERE revoked = 0 ORDER BY address")];
    return rows.map((r) => r.address);
  }
  _assignNextAddress() {
    const subnet = this._getSubnet();
    const assigned = this._getAssignedAddresses();
    const [baseIP, prefixStr] = subnet.split("/");
    const prefix = parseInt(prefixStr, 10);
    const baseParts = baseIP.split(".").map(Number);
    const baseNum = baseParts[0] << 24 | baseParts[1] << 16 | baseParts[2] << 8 | baseParts[3];
    const hostBits = 32 - prefix;
    const maxHosts = (1 << hostBits) - 2;
    const usedHosts = /* @__PURE__ */ new Set();
    for (const addr of assigned) {
      const addrIP = addr.split("/")[0];
      const parts = addrIP.split(".").map(Number);
      const num = parts[0] << 24 | parts[1] << 16 | parts[2] << 8 | parts[3];
      usedHosts.add(num - baseNum);
    }
    for (let h = 1; h <= maxHosts; h++) {
      if (!usedHosts.has(h)) {
        const addr = baseNum + h;
        const ip = `${addr >>> 24 & 255}.${addr >>> 16 & 255}.${addr >>> 8 & 255}.${addr & 255}`;
        return `${ip}/${prefix}`;
      }
    }
    return null;
  }
  async _getOrCreateSigningKey() {
    this._ensureTables();
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT kid, secret_hex FROM signing_keys WHERE revoked_at IS NULL ORDER BY created_at DESC LIMIT 1"
      )
    ];
    if (rows.length > 0) {
      return { kid: rows[0].kid, secretHex: rows[0].secret_hex };
    }
    const keyBytes = new Uint8Array(32);
    crypto.getRandomValues(keyBytes);
    const secretHex = Array.from(keyBytes).map((b) => b.toString(16).padStart(2, "0")).join("");
    const kidBytes = new Uint8Array(8);
    crypto.getRandomValues(kidBytes);
    const kid = "k_" + Array.from(kidBytes).map((b) => b.toString(16).padStart(2, "0")).join("");
    const now = Math.floor(Date.now() / 1e3);
    this.ctx.storage.sql.exec(
      "INSERT INTO signing_keys (kid, secret_hex, created_at) VALUES (?, ?, ?)",
      kid,
      secretHex,
      now
    );
    return { kid, secretHex };
  }
  async _signJWT(payload) {
    const getSigningKey = async () => this._getOrCreateSigningKey();
    return auth.signJWT(payload, getSigningKey);
  }
  async _verifyJWT(token) {
    const getSigningKey = async (kid) => {
      this._ensureTables();
      const rows = [
        ...this.ctx.storage.sql.exec(
          "SELECT kid, secret_hex FROM signing_keys WHERE kid = ? AND revoked_at IS NULL",
          kid
        )
      ];
      if (rows.length === 0) return null;
      return { kid: rows[0].kid, secretHex: rows[0].secret_hex };
    };
    return auth.verifyJWT(token, getSigningKey);
  }
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
    const ghResp = await fetch("https://api.github.com/user", {
      headers: {
        Authorization: `Bearer ${github_token}`,
        "User-Agent": "bamgate-worker",
        "Accept": "application/vnd.github+json"
      }
    });
    if (!ghResp.ok) {
      return this._jsonError("invalid GitHub token", 401);
    }
    const ghUser = await ghResp.json();
    const githubId = String(ghUser.id);
    const username = ghUser.login;
    this._ensureTables();
    const owners = [...this.ctx.storage.sql.exec("SELECT github_id FROM owners")];
    if (owners.length === 0) {
      const now2 = Math.floor(Date.now() / 1e3);
      this.ctx.storage.sql.exec(
        "INSERT INTO owners (github_id, username, created_at) VALUES (?, ?, ?)",
        githubId,
        username,
        now2
      );
    } else if (owners[0].github_id !== githubId) {
      return this._jsonError("unauthorized: you are not the owner of this network", 403);
    }
    const existing = [
      ...this.ctx.storage.sql.exec(
        "SELECT device_id, address FROM devices WHERE device_name = ? AND owner_github_id = ? AND revoked = 0",
        device_name,
        githubId
      )
    ];
    const now = Math.floor(Date.now() / 1e3);
    const refreshToken = auth.generateRefreshToken();
    const refreshTokenHash = await auth.hashToken(refreshToken);
    const refreshExpiresAt = now + 30 * 24 * 60 * 60;
    let deviceId;
    let address;
    if (existing.length > 0) {
      deviceId = existing[0].device_id;
      address = existing[0].address;
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
      exp: now + 3600
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
      server_url: serverURL
    });
  }
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
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT * FROM devices WHERE device_id = ? AND revoked = 0",
        device_id
      )
    ];
    if (rows.length === 0) {
      return this._jsonError("device not found or revoked", 401);
    }
    const device = rows[0];
    const now = Math.floor(Date.now() / 1e3);
    if (now > device.refresh_token_expires_at) {
      return this._jsonError("refresh_token_expired", 401);
    }
    const providedHash = await auth.hashToken(refresh_token);
    const matchesCurrent = providedHash === device.refresh_token_hash;
    const matchesPrev = device.prev_refresh_token_hash !== null && providedHash === device.prev_refresh_token_hash;
    if (!matchesCurrent && !matchesPrev) {
      return this._jsonError("invalid refresh token", 401);
    }
    const newRefreshToken = auth.generateRefreshToken();
    const newHash = await auth.hashToken(newRefreshToken);
    const newExpiresAt = now + 30 * 24 * 60 * 60;
    if (matchesCurrent) {
      this.ctx.storage.sql.exec(
        "UPDATE devices SET prev_refresh_token_hash = refresh_token_hash, refresh_token_hash = ?, refresh_token_expires_at = ?, last_seen_at = ? WHERE device_id = ?",
        newHash,
        newExpiresAt,
        now,
        device_id
      );
    } else {
      this.ctx.storage.sql.exec(
        "UPDATE devices SET prev_refresh_token_hash = NULL, refresh_token_hash = ?, refresh_token_expires_at = ?, last_seen_at = ? WHERE device_id = ?",
        newHash,
        newExpiresAt,
        now,
        device_id
      );
    }
    const accessToken = await this._signJWT({
      sub: device_id,
      owner: device.owner_github_id,
      net: "default",
      iat: now,
      exp: now + 3600
    });
    return this._jsonResponse({
      access_token: accessToken,
      refresh_token: newRefreshToken,
      expires_in: 3600
    });
  }
  async _handleListDevices(claims) {
    this._ensureTables();
    const rows = [
      ...this.ctx.storage.sql.exec(
        "SELECT device_id, device_name, address, created_at, last_seen_at, revoked FROM devices WHERE owner_github_id = ? ORDER BY created_at",
        claims.owner
      )
    ];
    return this._jsonResponse({
      devices: rows.map((r) => ({
        device_id: r.device_id,
        device_name: r.device_name,
        address: r.address,
        created_at: r.created_at,
        last_seen_at: r.last_seen_at,
        revoked: r.revoked === 1
      }))
    });
  }
  async _handleRevokeDevice(claims, targetDeviceId) {
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
      )
    ];
    if (rows.length === 0) {
      return this._jsonError("device not found", 404);
    }
    this.ctx.storage.sql.exec("UPDATE devices SET revoked = 1 WHERE device_id = ?", targetDeviceId);
    return this._jsonResponse({ ok: true });
  }
  _jsonResponse(data, status = 200) {
    return new Response(JSON.stringify(data), {
      status,
      headers: { "content-type": "application/json" }
    });
  }
  _jsonError(error, status) {
    return new Response(JSON.stringify({ error }), {
      status,
      headers: { "content-type": "application/json" }
    });
  }
};
export {
  SignalingRoom,
  worker_default as default
};
