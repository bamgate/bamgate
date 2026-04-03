import type { JWTPayload } from "./types.js";

function base64urlEncode(data: Uint8Array | string): string {
  const bytes = data instanceof Uint8Array ? data : new TextEncoder().encode(data);
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64urlDecode(str: string): Uint8Array {
  str = str.replace(/-/g, "+").replace(/_/g, "/");
  while (str.length % 4 !== 0) str += "=";
  const binary = atob(str);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function hexEncode(bytes: Uint8Array): string {
  return Array.from(bytes).map((b) => b.toString(16).padStart(2, "0")).join("");
}

function hexDecode(hex: string): Uint8Array {
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substring(i, i + 2), 16);
  }
  return bytes;
}

export interface SigningKey {
  kid: string;
  secretHex: string;
}

export class Auth {
  private keyCache: Map<string, CryptoKey> = new Map();

  async importHMACKey(secretHex: string): Promise<CryptoKey> {
    if (this.keyCache.has(secretHex)) {
      return this.keyCache.get(secretHex)!;
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

  async signJWT(
    payload: JWTPayload,
    getSigningKey: () => Promise<SigningKey>
  ): Promise<string> {
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

  async verifyJWT(
    token: string,
    getSigningKey: (kid: string) => Promise<SigningKey | null>
  ): Promise<JWTPayload | null> {
    const parts = token.split(".");
    if (parts.length !== 3) return null;

    let header: { alg?: string; kid?: string };
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

    let payload: JWTPayload;
    try {
      payload = JSON.parse(new TextDecoder().decode(base64urlDecode(parts[1])));
    } catch {
      return null;
    }

    const now = Math.floor(Date.now() / 1000);
    if (payload.exp && payload.exp < now) return null;

    return payload;
  }

  generateRefreshToken(): string {
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    return "bgr_" + hexEncode(bytes);
  }

  async hashToken(token: string): Promise<string> {
    const data = new TextEncoder().encode(token);
    const hash = await crypto.subtle.digest("SHA-256", data);
    return hexEncode(new Uint8Array(hash));
  }

  async verifyGitHubToken(githubToken: string): Promise<{ id: string; login: string } | null> {
    const resp = await fetch("https://api.github.com/user", {
      headers: {
        Authorization: `Bearer ${githubToken}`,
        "User-Agent": "bamgate-worker",
        Accept: "application/vnd.github+json",
      },
    });

    if (!resp.ok) return null;

    const user = (await resp.json()) as { id: string | number; login: string };
    return { id: String(user.id), login: user.login };
  }
}

export const auth = new Auth();
