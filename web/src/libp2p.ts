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
