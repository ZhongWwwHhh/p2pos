import { createLibp2p } from "libp2p";
import { webSockets } from "@libp2p/websockets";
import { noise } from "@chainsafe/libp2p-noise";
import { mplex } from "@libp2p/mplex";
import { yamux } from "@chainsafe/libp2p-yamux";
import { privateKeyFromProtobuf } from "@libp2p/crypto/keys";
import { multiaddr } from "@multiformats/multiaddr";
import { fromString, toString } from "uint8arrays";
import { concat } from "uint8arrays/concat";

const MEMBERSHIP_PUSH_PROTOCOL = "/p2pos/membership-push/1.0.0";
const MEMBERSHIP_PROTOCOL = "/p2pos/membership/1.0.0";
const STATUS_PROTOCOL = "/p2pos/status/1.0.0";

export type Libp2pClient = Awaited<ReturnType<typeof createLibp2p>>;

export async function createClient(privKeyB64: string) {
  const privKeyBytes = fromString(privKeyB64, "base64");
  const privKey = privateKeyFromProtobuf(privKeyBytes);

  const node = await createLibp2p({
    privateKey: privKey,
    transports: [webSockets()],
    connectionEncrypters: [noise()],
    streamMuxers: [yamux(), mplex()]
  });

  await node.start();
  return node;
}

export async function connect(node: Libp2pClient, addr: string) {
  return node.dial(multiaddr(addr));
}

export async function disconnect(node: Libp2pClient) {
  await node.stop();
}

export async function pushMembershipSnapshot(
  node: Libp2pClient,
  peerAddr: string,
  snapshotJson: string
) {
  const stream = await node.dialProtocol(multiaddr(peerAddr), MEMBERSHIP_PUSH_PROTOCOL);

  stream.send(fromString(snapshotJson));
  await stream.close();

  const chunks: Uint8Array[] = [];
  for await (const chunk of stream) {
    chunks.push(chunk.subarray());
  }
  const response = toString(concat(chunks));

  return response;
}

export type MembershipSnapshot = {
  cluster_id: string;
  issued_at: string;
  issuer_peer_id: string;
  members: string[];
  admin_proof: Record<string, unknown>;
  sig: string;
};

export type MembershipResponse = {
  snapshot?: MembershipSnapshot;
  error?: string;
};

export async function fetchMembershipSnapshot(
  node: Libp2pClient,
  peerAddr: string
): Promise<MembershipResponse> {
  const stream = await node.dialProtocol(multiaddr(peerAddr), MEMBERSHIP_PROTOCOL);
  await stream.close();

  const chunks: Uint8Array[] = [];
  for await (const chunk of stream) {
    chunks.push(chunk.subarray());
  }
  const raw = toString(concat(chunks));
  if (raw.trim() === "") {
    throw new Error("empty membership response");
  }
  return JSON.parse(raw) as MembershipResponse;
}

export type StatusRecord = {
  peer_id: string;
  last_remote_addr: string;
  last_seen_at: string;
  reachability: string;
  observed_by: string;
};

export type StatusResponse = {
  generated_at: string;
  peers: StatusRecord[];
  error?: string;
};

export async function fetchClusterStatus(node: Libp2pClient, peerAddr: string): Promise<StatusResponse> {
  const stream = await node.dialProtocol(multiaddr(peerAddr), STATUS_PROTOCOL);
  stream.send(fromString(JSON.stringify({ scope: "cluster" })));
  await stream.close();

  const chunks: Uint8Array[] = [];
  for await (const chunk of stream) {
    chunks.push(chunk.subarray());
  }
  const raw = toString(concat(chunks));
  if (raw.trim() === "") {
    throw new Error("empty status response");
  }

  const resp = JSON.parse(raw) as StatusResponse;
  return {
    generated_at: resp.generated_at ?? "",
    peers: Array.isArray(resp.peers) ? resp.peers : [],
    error: resp.error
  };
}
