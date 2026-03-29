import { describe, it, expect } from "vitest";
import {
  Method,
  Class,
  Attr,
  newBuilder,
  newResponse,
  parse,
  isSTUN,
  isChannelData,
  parseChannelData,
  buildChannelData,
  getUsername,
  getRealm,
  getNonce,
  getLifetime,
  getChannelNumber,
  getData,
  getXORPeerAddresses,
  getXORRelayedAddress,
  checkIntegrity,
  checkFingerprint,
  deriveAuthKey,
  ipToString,
  parseIPv4,
  type Message,
  type XORAddress,
} from "../src/stun";

describe("STUN Message Type", () => {
  it("round trips for various method/class combinations", async () => {
    const cases = [
      { method: Method.Binding, classVal: Class.Request },
      { method: Method.Binding, classVal: Class.SuccessResponse },
      { method: Method.Allocate, classVal: Class.Request },
      { method: Method.Allocate, classVal: Class.SuccessResponse },
      { method: Method.Allocate, classVal: Class.ErrorResponse },
      { method: Method.Refresh, classVal: Class.Request },
      { method: Method.Send, classVal: Class.Indication },
      { method: Method.Data, classVal: Class.Indication },
      { method: Method.CreatePermission, classVal: Class.Request },
      { method: Method.ChannelBind, classVal: Class.Request },
    ];

    for (const { method, classVal } of cases) {
      const txID = new Uint8Array(12);
      const built = await newBuilder(method, classVal, txID).buildAsync(null);
      const msg = parse(built);
      expect(msg.method).toBe(method);
      expect(msg.class).toBe(classVal);
    }
  });
});

describe("STUN Binding Request", () => {
  it("parses and builds correctly", async () => {
    const txID = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]);
    const built = await newBuilder(Method.Binding, Class.Request, txID).buildAsync(null);

    expect(isSTUN(built)).toBe(true);
    expect(isChannelData(built)).toBe(false);

    const msg = parse(built);
    expect(msg.method).toBe(Method.Binding);
    expect(msg.class).toBe(Class.Request);
    expect(Array.from(msg.transactionID)).toEqual(Array.from(txID));
  });
});

describe("STUN Allocate Error Response", () => {
  it("parses error code, realm, and nonce", async () => {
    const txID = new Uint8Array([0xaa, 0xbb, 0xcc, 0xdd, 0, 0, 0, 0, 0, 0, 0, 0]);
    const built = await newBuilder(Method.Allocate, Class.ErrorResponse, txID)
      .addErrorCode(401, "Unauthorized")
      .addRealm("bamgate")
      .addNonce("test-nonce-123")
      .buildAsync(null);

    const msg = parse(built);
    expect(msg.method).toBe(Method.Allocate);
    expect(msg.class).toBe(Class.ErrorResponse);

    const err = getErrorCode(msg);
    expect(err?.code).toBe(401);

    expect(getRealm(msg)).toBe("bamgate");
    expect(getNonce(msg)).toBe("test-nonce-123");
  });
});

function getErrorCode(msg: Message): { code: number; reason: string } | null {
  const v = msg.attributes.find((a) => a.type === Attr.ErrorCode);
  if (!v || v.value.length < 4) return null;
  const classDigit = v.value[2];
  const numberDigit = v.value[3];
  const code = classDigit * 100 + numberDigit;
  const reason = new TextDecoder().decode(v.value.subarray(4));
  return { code, reason };
}

describe("XOR Address IPv4", () => {
  it("round trips IPv4 addresses", async () => {
    const txID = new Uint8Array([0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c]);
    const addr: XORAddress = { ip: parseIPv4("192.168.1.1")!, port: 50000 };

    const built = await newBuilder(Method.Allocate, Class.SuccessResponse, txID)
      .addXORAddress(Attr.XORRelayedAddress, addr)
      .buildAsync(null);

    const msg = parse(built);
    const relayed = getXORRelayedAddress(msg);
    expect(relayed).not.toBeNull();
    expect(ipToString(relayed!.ip)).toBe("192.168.1.1");
    expect(relayed!.port).toBe(50000);
  });
});

describe("XOR Address IPv6", () => {
  it("round trips IPv6 addresses", async () => {
    const txID = new Uint8Array([0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66]);
    const ip6 = parseIPv6("2001:db8::1");
    const addr: XORAddress = { ip: ip6, port: 3478 };

    const built = await newBuilder(Method.Allocate, Class.SuccessResponse, txID)
      .addXORAddress(Attr.XORRelayedAddress, addr)
      .buildAsync(null);

    const msg = parse(built);
    const relayed = getXORRelayedAddress(msg);
    expect(relayed).not.toBeNull();
    expect(Array.from(relayed!.ip)).toEqual(Array.from(ip6));
    expect(relayed!.port).toBe(3478);
  });
});

function parseIPv6(s: string): Uint8Array {
  const parts = s.split(":");
  const result: number[] = [];
  for (let i = 0; i < 8; i++) {
    let part = parts[i] || "0";
    if (part === "") {
      const missing = 8 - parts.filter((p) => p).length;
      for (let j = 0; j < missing; j++) {
        result.push(0, 0);
      }
      continue;
    }
    const value = parseInt(part, 16);
    result.push((value >> 8) & 0xff, value & 0xff);
  }
  return new Uint8Array(result);
}

describe("Message Integrity", () => {
  it("validates correct integrity", async () => {
    const txID = new Uint8Array(12);
    const authKey = deriveAuthKey("user", "realm", "pass");

    const built = await newBuilder(Method.Allocate, Class.SuccessResponse, txID)
      .addLifetime(600)
      .addXORAddress(Attr.XORRelayedAddress, { ip: parseIPv4("10.0.0.1")!, port: 50000 })
      .buildAsync(authKey);

    const err = await checkIntegrity(built, authKey);
    expect(err).toBeNull();
  });

  it("rejects wrong key", async () => {
    const txID = new Uint8Array(12);
    const authKey = deriveAuthKey("user", "realm", "pass");
    const wrongKey = deriveAuthKey("user", "realm", "wrong");

    const built = await newBuilder(Method.Allocate, Class.SuccessResponse, txID)
      .addLifetime(600)
      .buildAsync(authKey);

    const err = await checkIntegrity(built, wrongKey);
    expect(err).not.toBeNull();
  });
});

describe("Fingerprint", () => {
  it("validates correct fingerprint", async () => {
    const txID = new Uint8Array([0x42, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    const built = await newBuilder(Method.Binding, Class.Request, txID).buildAsync(null);

    const err = checkFingerprint(built);
    expect(err).toBeNull();
  });

  it("rejects tampered fingerprint", async () => {
    const txID = new Uint8Array([0x42, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    const built = await newBuilder(Method.Binding, Class.Request, txID).buildAsync(null);

    built[20] ^= 0xff;

    const err = checkFingerprint(built);
    expect(err).not.toBeNull();
  });
});

describe("ChannelData", () => {
  it("round trips", () => {
    const payload = new TextEncoder().encode("hello, turn relay");
    const ch = 0x4000;

    const frame = buildChannelData(ch, payload);

    expect(isChannelData(frame)).toBe(true);
    expect(isSTUN(frame)).toBe(false);

    const parsed = parseChannelData(frame);
    expect(parsed.channelNumber).toBe(ch);
    expect(new TextDecoder().decode(parsed.data)).toBe("hello, turn relay");
  });

  it("handles padding correctly", () => {
    const payload = new Uint8Array([0x01, 0x02, 0x03]);
    const frame = buildChannelData(0x4001, payload);

    expect(frame.length).toBe(8);

    const len = (frame[2] << 8) | frame[3];
    expect(len).toBe(3);

    const parsed = parseChannelData(frame);
    expect(parsed.data.length).toBe(3);
  });
});

describe("isSTUN", () => {
  it("rejects too short data", () => {
    expect(isSTUN(new Uint8Array([0, 0, 0]))).toBe(false);
  });
});

describe("isChannelData", () => {
  it("rejects too short data", () => {
    expect(isChannelData(new Uint8Array([0x40]))).toBe(false);
  });
});

describe("parse", () => {
  it("rejects too short message", () => {
    expect(() => parse(new Uint8Array([0, 0]))).toThrow();
  });
});

describe("getXORPeerAddresses", () => {
  it("returns multiple addresses", async () => {
    const txID = new Uint8Array(12);
    const addr1: XORAddress = { ip: parseIPv4("10.0.0.1")!, port: 3478 };
    const addr2: XORAddress = { ip: parseIPv4("10.0.0.2")!, port: 3479 };

    const built = await newBuilder(Method.CreatePermission, Class.Request, txID)
      .addXORAddress(Attr.XORPeerAddress, addr1)
      .addXORAddress(Attr.XORPeerAddress, addr2)
      .buildAsync(null);

    const msg = parse(built);
    const addrs = getXORPeerAddresses(msg);

    expect(addrs.length).toBe(2);
    expect(ipToString(addrs[0].ip)).toBe("10.0.0.1");
    expect(addrs[0].port).toBe(3478);
    expect(ipToString(addrs[1].ip)).toBe("10.0.0.2");
    expect(addrs[1].port).toBe(3479);
  });
});

describe("Lifetime", () => {
  it("round trips", async () => {
    const txID = new Uint8Array(12);
    const built = await newBuilder(Method.Refresh, Class.Request, txID)
      .addLifetime(3600)
      .buildAsync(null);

    const msg = parse(built);
    expect(getLifetime(msg)).toBe(3600);
  });
});

describe("ChannelNumber", () => {
  it("round trips", async () => {
    const txID = new Uint8Array(12);
    const built = await newBuilder(Method.ChannelBind, Class.Request, txID)
      .addChannelNumber(0x4000)
      .buildAsync(null);

    const msg = parse(built);
    expect(getChannelNumber(msg)).toBe(0x4000);
  });
});

describe("Data", () => {
  it("round trips", async () => {
    const txID = new Uint8Array(12);
    const payload = new Uint8Array([0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe]);

    const built = await newBuilder(Method.Send, Class.Indication, txID)
      .addData(payload)
      .addXORAddress(Attr.XORPeerAddress, { ip: parseIPv4("10.0.0.1")!, port: 50000 })
      .buildNoFingerprintAsync(null);

    const msg = parse(built);
    const data = getData(msg);

    expect(data).not.toBeNull();
    expect(data!.length).toBe(payload.length);
    expect(Array.from(data!)).toEqual(Array.from(payload));
  });
});

describe("Allocate Dance", () => {
  it("full TURN allocate two-phase dance", async () => {
    const txID1 = new Uint8Array([0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c]);
    const req1 = await newBuilder(Method.Allocate, Class.Request, txID1)
      .addRaw(Attr.RequestedTransport, new Uint8Array([0x11, 0, 0, 0]))
      .buildAsync(null);

    const msg1 = parse(req1);
    expect(msg1.method).toBe(Method.Allocate);
    expect(msg1.class).toBe(Class.Request);
    expect(getUsername(msg1)).toBe("");

    const resp1 = await newResponse(msg1, Class.ErrorResponse)
      .addErrorCode(401, "Unauthorized")
      .addRealm("bamgate")
      .addNonce("test-nonce-abc")
      .buildAsync(null);

    const respMsg1 = parse(resp1);
    expect(respMsg1.class).toBe(Class.ErrorResponse);

    const err1 = getErrorCode(respMsg1);
    expect(err1?.code).toBe(401);
    expect(getRealm(respMsg1)).toBe("bamgate");
    expect(getNonce(respMsg1)).toBe("test-nonce-abc");

    const authKey = deriveAuthKey("user123", "bamgate", "password123");
    const txID2 = new Uint8Array([0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c]);
    const req2 = await newBuilder(Method.Allocate, Class.Request, txID2)
      .addRaw(Attr.RequestedTransport, new Uint8Array([0x11, 0, 0, 0]))
      .addUsername("user123")
      .addRealm("bamgate")
      .addNonce("test-nonce-abc")
      .buildAsync(authKey);

    const integrityErr = await checkIntegrity(req2, authKey);
    expect(integrityErr).toBeNull();

    const fpErr = checkFingerprint(req2);
    expect(fpErr).toBeNull();

    const msg2 = parse(req2);
    expect(getUsername(msg2)).toBe("user123");

    const relayAddr: XORAddress = { ip: parseIPv4("10.255.0.1")!, port: 50000 };
    const resp2 = await newResponse(msg2, Class.SuccessResponse)
      .addXORAddress(Attr.XORRelayedAddress, relayAddr)
      .addXORAddress(Attr.XORMappedAddress, { ip: parseIPv4("203.0.113.1")!, port: 12345 })
      .addLifetime(600)
      .buildAsync(authKey);

    const integrityErr2 = await checkIntegrity(resp2, authKey);
    expect(integrityErr2).toBeNull();

    const respMsg2 = parse(resp2);
    expect(respMsg2.class).toBe(Class.SuccessResponse);
    expect(getLifetime(respMsg2)).toBe(600);

    const relayed = getXORRelayedAddress(respMsg2);
    expect(relayed).not.toBeNull();
    expect(ipToString(relayed!.ip)).toBe("10.255.0.1");
    expect(relayed!.port).toBe(50000);
  });
});

describe("BuildNoFingerprint", () => {
  it("does not include fingerprint", async () => {
    const txID = new Uint8Array([0x42, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    const built = await newBuilder(Method.Send, Class.Indication, txID)
      .addData(new TextEncoder().encode("test"))
      .buildNoFingerprintAsync(null);

    if (built.length >= 8) {
      const lastAttrType = (built[built.length - 8] << 8) | built[built.length - 7];
      expect(lastAttrType).not.toBe(Attr.Fingerprint);
    }

    const msg = parse(built);
    expect(msg.method).toBe(Method.Send);
    expect(msg.class).toBe(Class.Indication);
  });
});
