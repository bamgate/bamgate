import type { PeerInfo, SignalingMessage } from "./types.js";

interface Peer {
  wsId: number;
  peerId: string;
  publicKey: string;
  address: string;
  routes: string[];
  metadata: Record<string, string>;
}

export interface SendFn {
  (wsId: number, data: string): void;
}

export class Hub {
  private peers: Map<number, Peer> = new Map();
  private peerById: Map<string, number> = new Map();
  private sendFn: SendFn;

  constructor(sendFn: SendFn) {
    this.sendFn = sendFn;
  }

  rehydrate(
    wsId: number,
    peerId: string,
    publicKey: string,
    address: string,
    routes: string[],
    metadata: Record<string, string>
  ): void {
    const peer: Peer = {
      wsId,
      peerId,
      publicKey,
      address,
      routes,
      metadata,
    };
    this.peers.set(wsId, peer);
    this.peerById.set(peerId, wsId);
  }

  join(
    wsId: number,
    peerId: string,
    publicKey: string,
    address: string,
    routes: string[],
    metadata: Record<string, string>
  ): PeerInfo[] {
    const peer: Peer = {
      wsId,
      peerId,
      publicKey,
      address,
      routes,
      metadata,
    };

    const peerInfos: PeerInfo[] = [];
    for (const existing of this.peers.values()) {
      peerInfos.push({
        peerId: existing.peerId,
        publicKey: existing.publicKey,
        address: existing.address,
        routes: existing.routes,
        metadata: existing.metadata,
      });
    }

    this.peers.set(wsId, peer);
    this.peerById.set(peerId, wsId);

    return peerInfos;
  }

  message(_wsId: number, rawJson: string): void {
    let msg: SignalingMessage;
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
      if (targetWsId !== undefined) {
        this.sendFn(targetWsId, rawJson);
      }
    }
  }

  leave(wsId: number): string | null {
    const peer = this.peers.get(wsId);
    if (!peer) return null;

    const peerId = peer.peerId;
    this.peers.delete(wsId);
    this.peerById.delete(peerId);

    return peerId;
  }

  broadcast(senderWsId: number, data: string): void {
    for (const [id] of this.peers) {
      if (id === senderWsId) continue;
      this.sendFn(id, data);
    }
  }

  getPeers(): PeerInfo[] {
    const peerInfos: PeerInfo[] = [];
    for (const peer of this.peers.values()) {
      peerInfos.push({
        peerId: peer.peerId,
        publicKey: peer.publicKey,
        address: peer.address,
        routes: peer.routes,
        metadata: peer.metadata,
      });
    }
    return peerInfos;
  }

  getPeer(wsId: number): Peer | undefined {
    return this.peers.get(wsId);
  }
}

export function createHub(sendFn: SendFn): Hub {
  return new Hub(sendFn);
}
