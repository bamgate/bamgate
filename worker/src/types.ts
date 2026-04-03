export interface PeerInfo {
  peerId: string;
  publicKey: string;
  address?: string;
  routes?: string[];
  metadata?: Record<string, string>;
}

export interface JoinMessage {
  type: "join";
  peerId: string;
  publicKey?: string;
  address?: string;
  routes?: string[];
  metadata?: Record<string, string>;
}

export interface PeersMessage {
  type: "peers";
  peers: PeerInfo[];
}

export interface PeerLeftMessage {
  type: "peer-left";
  peerId: string;
}

export interface OfferMessage {
  type: "offer";
  to: string;
  sdp: string;
}

export interface AnswerMessage {
  type: "answer";
  to: string;
  sdp: string;
}

export interface IceCandidateMessage {
  type: "ice-candidate";
  to: string;
  candidate: string;
}

export type SignalingMessage =
  | JoinMessage
  | PeersMessage
  | PeerLeftMessage
  | OfferMessage
  | AnswerMessage
  | IceCandidateMessage;

export interface JWTPayload {
  sub: string;
  owner: string;
  net: string;
  iat: number;
  exp: number;
}

export interface Device {
  device_id: string;
  device_name: string;
  address: string;
  created_at: number;
  last_seen_at?: number;
  revoked: boolean;
}

export interface WebSocketAttachment {
  wsId: number;
  joined: boolean;
  isTurn: boolean;
  deviceId?: string;
  peerId?: string;
  publicKey?: string;
  address?: string;
  routes?: string[];
  metadata?: Record<string, string>;
  turnAlloc?: TurnAllocJSON;
}

export interface TurnAllocJSON {
  u: string;
  n: string;
  ak: string;
  ri: string;
  rp: number;
  rh: number;
  ca: number;
  lt: number;
  pm?: string[];
  ch?: Record<string, string>;
}

export interface Env {
  SIGNALING_ROOM: DurableObjectNamespace;
}
