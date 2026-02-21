<template>
  <div class="page">
    <div>
      <div class="title">P2POS Admin</div>
      <div class="subtitle">Cloudflare Worker + Web UI (browser libp2p client)</div>
    </div>

    <div class="grid">
      <section class="card">
        <h2>Connection</h2>
        <label>Bootstrap</label>
        <input :value="bootstrapAddr || '-'" readonly />
        <div class="hint">Auto normalized: {{ normalizedBootstrapAddr || "-" }}</div>
        <div class="hint">Cluster: {{ clusterId || "-" }}</div>

        <div class="row">
          <button class="btn" :disabled="!canConnect" @click="connectNode">Connect</button>
          <button class="btn secondary" :disabled="!client" @click="disconnectNode">Disconnect</button>
        </div>

        <div class="status" style="margin-top: 12px;">
          <span class="dot"></span>
          <span>State: {{ runtimeState }}</span>
        </div>
        <div v-if="connectError" class="error">{{ connectError }}</div>
        <div class="hint">先在 Admin Bundle 导入配置，再连接。</div>
      </section>

      <section class="card">
        <h2>Admin Bundle</h2>
        <label>Config Bundle</label>
        <textarea v-model="bundleInput" placeholder="p2pos-admin://..."></textarea>

        <div class="row">
          <button class="btn" :disabled="!canLoadBundle" @click="loadBundle">Load Bundle</button>
          <button class="btn secondary" @click="clearBundle">Clear</button>
        </div>

        <div v-if="credError" class="error">{{ credError }}</div>
        <div v-if="adminState" class="hint">Admin loaded: {{ adminState }}</div>
      </section>

      <section class="card">
        <h2>Membership List</h2>
        <div class="row" style="margin-bottom: 8px;">
          <button class="btn secondary" :disabled="members.length === 0" @click="clearMembers">Clear All</button>
        </div>
        <div class="list">
          <div v-for="id in members" :key="id" class="list-item">
            <span>{{ id }}</span>
            <button class="btn secondary list-remove-btn" @click="removeMember(id)">Delete</button>
          </div>
        </div>

        <label>Add Peer ID</label>
        <div class="row">
          <input v-model="newMember" placeholder="12D3KooW..." />
          <button class="btn" @click="addMember">Add</button>
        </div>

        <div class="row" style="margin-top: 8px;">
          <button class="btn" :disabled="!canPublish" @click="publishSnapshot">Publish Snapshot</button>
          <button class="btn danger" :disabled="true">Revoke Snapshot</button>
        </div>

        <div class="hint">发布会走 admin 签名并推送到 network。</div>
      </section>

      <section class="card">
        <h2>Snapshot Preview</h2>
        <label>Generated Snapshot (JSON)</label>
        <textarea readonly :value="snapshotJson"></textarea>
        <div class="row">
          <button class="btn secondary" @click="copySnapshot">Copy</button>
          <button class="btn secondary" @click="refreshIssuedAt">Refresh issued_at</button>
        </div>
        <div class="hint">当前快照仅生成本地 JSON，签名与推送走浏览器 libp2p。</div>
      </section>

      <section class="card">
        <h2>Status</h2>
        <div class="row">
          <button class="btn" :disabled="!client || statusLoading" @click="loadClusterStatus">Fetch Cluster Status</button>
          <button class="btn secondary" :disabled="!client" @click="toggleStatusAutoRefresh">
            {{ statusAutoRefresh ? "Stop Auto Refresh" : "Start Auto Refresh" }}
          </button>
        </div>
        <div class="hint" style="margin-top: 8px;">
          generated_at: {{ statusGeneratedAt || "-" }} | peers: {{ statusPeers.length }}
        </div>
        <div v-if="statusError" class="error">{{ statusError }}</div>

        <div class="status-table-wrap" style="margin-top: 12px;">
          <table class="status-table">
            <thead>
              <tr>
                <th>peer_id</th>
                <th>reachability</th>
                <th>last_seen_at</th>
                <th>observed_by</th>
                <th>last_remote_addr</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="peer in statusPeers" :key="peer.peer_id">
                <td>{{ peer.peer_id }}</td>
                <td>{{ peer.reachability }}</td>
                <td>{{ peer.last_seen_at }}</td>
                <td>{{ peer.observed_by }}</td>
                <td>{{ peer.last_remote_addr }}</td>
              </tr>
              <tr v-if="statusPeers.length === 0">
                <td colspan="5">No status records.</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import {
  connect,
  createClient,
  fetchMembershipSnapshot,
  fetchClusterStatus,
  pushMembershipSnapshot,
  type Libp2pClient,
  type StatusRecord
} from "./libp2p";
import { peerIdFromPrivateKey, peerIdFromString } from "@libp2p/peer-id";
import { privateKeyFromProtobuf } from "@libp2p/crypto/keys";
import { base36 } from "multiformats/bases/base36";

const bootstrapAddr = ref("/dns4/init.p2pos.zhongwwwhhh.cc/tcp/4100/p2p/");
const clusterId = ref("default");
const adminPrivKey = ref("");
const adminProofJson = ref("");
const bundleInput = ref("");
const members = ref<string[]>([]);
const newMember = ref("");
const runtimeState = ref("unconfigured");
const credError = ref("");
const adminState = ref("");
const issuedAt = ref(new Date().toISOString());
const client = ref<Libp2pClient | null>(null);
const connectError = ref("");
const connectedAddr = ref("");
const statusPeers = ref<StatusRecord[]>([]);
const statusGeneratedAt = ref("");
const statusError = ref("");
const statusLoading = ref(false);
const statusAutoRefresh = ref(false);
let statusTimer: ReturnType<typeof setInterval> | null = null;

type AdminProof = {
  cluster_id: string;
  peer_id: string;
  role: string;
  valid_from: string;
  valid_to: string;
  sig: string;
};

type AdminBundle = {
  v: number;
  cluster_id: string;
  bootstrap: string;
  admin_priv: string;
  admin_proof: AdminProof;
};

type MembershipSnapshot = {
  cluster_id: string;
  issued_at: string;
  issuer_peer_id: string;
  members: string[];
  admin_proof: AdminProof;
  sig: string;
};

const addMember = () => {
  const value = newMember.value.trim();
  if (!value) return;
  if (members.value.includes(value)) {
    newMember.value = "";
    return;
  }
  members.value.push(value);
  newMember.value = "";
};

const removeMember = (id: string) => {
  members.value = members.value.filter((item) => item !== id);
};

const clearMembers = () => {
  members.value = [];
};

const canLoadBundle = computed(() => {
  return bundleInput.value.trim().length > 0;
});

const canPublish = computed(() => {
  return members.value.length > 0 && adminState.value !== "" && client.value !== null;
});

const loadBundle = () => {
  credError.value = "";
  adminState.value = "";
  const parsed = parseBundle(bundleInput.value);
  if (!parsed) {
    credError.value = "Bundle invalid.";
    return;
  }
  clusterId.value = parsed.cluster_id || "default";
  bootstrapAddr.value = parsed.bootstrap || "";
  adminPrivKey.value = parsed.admin_priv || "";
  adminProofJson.value = JSON.stringify(parsed.admin_proof ?? {}, null, 2);

  const proof = parseAdminProof(adminProofJson.value);
  if (!proof) {
    credError.value = "Admin proof JSON invalid or missing fields.";
    return;
  }
  if (!isBase64(adminPrivKey.value.trim())) {
    credError.value = "Admin private key is not valid base64.";
    return;
  }
  adminState.value = proof.peer_id;
};

const clearBundle = () => {
  bundleInput.value = "";
  adminPrivKey.value = "";
  adminProofJson.value = "";
  bootstrapAddr.value = "";
  clusterId.value = "default";
  adminState.value = "";
  credError.value = "";
};

const snapshotJson = computed(() => {
  const proof = parseAdminProof(adminProofJson.value);
  return JSON.stringify(
    {
      cluster_id: clusterId.value.trim() || "default",
      issued_at: issuedAt.value,
      issuer_peer_id: adminState.value || "",
      members: members.value,
      admin_proof: proof ?? {},
      sig: ""
    },
    null,
    2
  );
});

const copySnapshot = async () => {
  try {
    await navigator.clipboard.writeText(snapshotJson.value);
  } catch {
    credError.value = "Copy failed. Check browser permissions.";
  }
};

const refreshIssuedAt = () => {
  issuedAt.value = new Date().toISOString();
};

const canConnect = computed(() => {
  return adminPrivKey.value.trim().length > 0 && bootstrapAddr.value.trim().length > 0 && !client.value;
});

const normalizedBootstrapAddr = computed(() => normalizeBootstrapAddr(bootstrapAddr.value));

const connectNode = async () => {
  connectError.value = "";
  try {
    const addr = normalizedBootstrapAddr.value;
    if (!addr) {
      throw new Error("bootstrap address is empty");
    }

    const candidates = await resolveBootstrapCandidates(addr);
    if (candidates.length === 0) {
      throw new Error("no browser-compatible bootstrap address found");
    }

    const node = await createClient(adminPrivKey.value.trim());
    let connected = "";
    let lastErr = "";
    for (const candidate of candidates) {
      try {
        await connect(node, candidate);
        connected = candidate;
        break;
      } catch (err) {
        lastErr = formatUnknownError(err, "connect failed");
      }
    }
    if (!connected) {
      await node.stop();
      throw new Error(lastErr || "connect failed");
    }
    client.value = node;
    connectedAddr.value = connected;
    bootstrapAddr.value = connected;
    runtimeState.value = "connected";
    await hydrateMembershipFromNode(node, connected);
  } catch (err) {
    connectError.value = formatUnknownError(err, "connect failed");
  }
};

const disconnectNode = async () => {
  if (!client.value) return;
  await client.value.stop();
  client.value = null;
  connectedAddr.value = "";
  runtimeState.value = "unconfigured";
  stopStatusAutoRefresh();
};

const publishSnapshot = async () => {
  if (!client.value) return;
  try {
    // Ensure each publish uses a strictly newer issued_at.
    issuedAt.value = new Date().toISOString();

    const addr = connectedAddr.value || normalizedBootstrapAddr.value;
    if (!addr) {
      throw new Error("bootstrap address is empty");
    }
    const snapshot = await buildSignedSnapshot();
    const rawResp = await pushMembershipSnapshot(client.value, addr, JSON.stringify(snapshot));
    const resp = parsePushResponse(rawResp);
    if (!resp.applied) {
      throw new Error(resp.error || "snapshot rejected");
    }
    runtimeState.value = "healthy";
  } catch (err) {
    connectError.value = formatUnknownError(err, "publish failed");
  }
};

const hydrateMembershipFromNode = async (node: Libp2pClient, addr: string) => {
  try {
    const resp = await fetchMembershipSnapshot(node, addr);
    if (resp.error && resp.error.trim() !== "") {
      return;
    }
    if (!resp.snapshot) {
      return;
    }

    if (typeof resp.snapshot.cluster_id === "string" && resp.snapshot.cluster_id.trim() !== "") {
      clusterId.value = resp.snapshot.cluster_id.trim();
    }
    if (Array.isArray(resp.snapshot.members)) {
      members.value = normalizeMembers(resp.snapshot.members);
    }
    if (typeof resp.snapshot.issued_at === "string" && resp.snapshot.issued_at.trim() !== "") {
      issuedAt.value = resp.snapshot.issued_at;
    }
  } catch {
    // Non-fatal: keep current local list when remote membership fetch fails.
  }
};

const loadClusterStatus = async () => {
  if (!client.value) return;
  statusError.value = "";
  statusLoading.value = true;
  try {
    const addr = connectedAddr.value || normalizedBootstrapAddr.value;
    if (!addr) {
      throw new Error("bootstrap address is empty");
    }
    const resp = await fetchClusterStatus(client.value, addr);
    if (resp.error && resp.error.trim() !== "") {
      throw new Error(resp.error);
    }
    statusGeneratedAt.value = resp.generated_at || "";
    statusPeers.value = resp.peers ?? [];
  } catch (err) {
    statusError.value = formatUnknownError(err, "status fetch failed");
  } finally {
    statusLoading.value = false;
  }
};

const toggleStatusAutoRefresh = () => {
  if (statusAutoRefresh.value) {
    stopStatusAutoRefresh();
    return;
  }
  if (!client.value) return;
  statusAutoRefresh.value = true;
  void loadClusterStatus();
  statusTimer = setInterval(() => {
    if (!client.value) return;
    void loadClusterStatus();
  }, 15000);
};

function stopStatusAutoRefresh() {
  statusAutoRefresh.value = false;
  if (statusTimer !== null) {
    clearInterval(statusTimer);
    statusTimer = null;
  }
}

onUnmounted(() => {
  stopStatusAutoRefresh();
});

async function buildSignedSnapshot(): Promise<MembershipSnapshot> {
  const proof = parseAdminProof(adminProofJson.value);
  if (!proof) {
    throw new Error("admin proof invalid");
  }

  const cluster = clusterId.value.trim() || "default";
  if (proof.cluster_id !== cluster) {
    throw new Error("admin proof cluster_id mismatch");
  }

  const issuedDate = new Date(issuedAt.value);
  if (Number.isNaN(issuedDate.getTime())) {
    throw new Error("issued_at invalid");
  }
  const issuedAtRFC3339Nano = formatRFC3339NanoUTC(issuedDate);

  const priv = privateKeyFromProtobuf(base64ToUint8(adminPrivKey.value.trim()));
  const issuerPeerID = peerIdFromPrivateKey(priv).toString();
  if (proof.peer_id !== issuerPeerID) {
    throw new Error("admin proof peer_id does not match private key");
  }

  const normalizedMembers = normalizeMembers(members.value);
  if (normalizedMembers.length === 0) {
    throw new Error("members is empty");
  }

  const canonical = `${cluster}|${issuedAtRFC3339Nano}|${issuerPeerID}|${normalizedMembers.join(",")}`;
  const sigBytes = await Promise.resolve(priv.sign(new TextEncoder().encode(canonical)));

  return {
    cluster_id: cluster,
    issued_at: issuedAtRFC3339Nano,
    issuer_peer_id: issuerPeerID,
    members: normalizedMembers,
    admin_proof: proof,
    sig: uint8ToBase64(sigBytes)
  };
}

function parsePushResponse(raw: string): { applied: boolean; error?: string } {
  const text = raw.trim();
  if (text === "") {
    return { applied: true };
  }
  try {
    const obj = JSON.parse(text) as { applied?: boolean; error?: string };
    return { applied: obj.applied === true, error: obj.error };
  } catch {
    return { applied: false, error: text };
  }
}

function parseAdminProof(raw: string): AdminProof | null {
  try {
    const obj = JSON.parse(raw) as AdminProof;
    if (
      !obj ||
      !obj.cluster_id ||
      !obj.peer_id ||
      !obj.role ||
      !obj.valid_from ||
      !obj.valid_to ||
      !obj.sig
    ) {
      return null;
    }
    return obj;
  } catch {
    return null;
  }
}

function isBase64(val: string): boolean {
  try {
    return btoa(atob(val)) === val;
  } catch {
    return false;
  }
}

function base64ToUint8(val: string): Uint8Array {
  const bin = atob(val);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

function uint8ToBase64(data: Uint8Array): string {
  let bin = "";
  for (let i = 0; i < data.length; i++) {
    bin += String.fromCharCode(data[i]);
  }
  return btoa(bin);
}

function normalizeMembers(values: string[]): string[] {
  const uniq = new Set<string>();
  for (const raw of values) {
    const v = raw.trim();
    if (v !== "") {
      uniq.add(v);
    }
  }
  return Array.from(uniq).sort();
}

function formatRFC3339NanoUTC(d: Date): string {
  const pad2 = (n: number) => String(n).padStart(2, "0");
  const year = d.getUTCFullYear();
  const month = pad2(d.getUTCMonth() + 1);
  const day = pad2(d.getUTCDate());
  const hour = pad2(d.getUTCHours());
  const min = pad2(d.getUTCMinutes());
  const sec = pad2(d.getUTCSeconds());
  const ms = d.getUTCMilliseconds();

  if (ms === 0) {
    return `${year}-${month}-${day}T${hour}:${min}:${sec}Z`;
  }

  const nanos = String(ms * 1_000_000).padStart(9, "0").replace(/0+$/, "");
  return `${year}-${month}-${day}T${hour}:${min}:${sec}.${nanos}Z`;
}

function normalizeBootstrapAddr(raw: string): string {
  const value = raw.trim();
  if (!value) {
    return "";
  }

  // Allow plain domain input, resolve via dnsaddr records.
  if (!value.startsWith("/")) {
    return `/dnsaddr/${value}`;
  }

  if (value.includes("/ws") || value.includes("/wss") || value.includes("/webtransport")) {
    return value;
  }

  // Convert /ip4/<ip>/tcp/<port>/p2p/<peer> into forge-compatible
  // /ip4/<ip>/tcp/<port>/tls/sni/<escaped-ip>.<peer-cid36>.libp2p.direct/ws/p2p/<peer>
  const ip4Match = value.match(/^\/ip4\/([^/]+)\/tcp\/(\d+)\/p2p\/([^/]+)$/);
  if (ip4Match) {
    const ip = ip4Match[1];
    const port = ip4Match[2];
    const peerID = ip4Match[3];
    const escapedIP = ip.replaceAll(".", "-");
    try {
      const pid = peerIdFromString(peerID);
      const peerCID36 = pid.toCID().toString(base36);
      const sni = `${escapedIP}.${peerCID36}.libp2p.direct`;
      return `/ip4/${ip}/tcp/${port}/tls/sni/${sni}/ws/p2p/${peerID}`;
    } catch {
      // Fallback to generic tls/ws if peer id parsing fails.
      return `/ip4/${ip}/tcp/${port}/tls/ws/p2p/${peerID}`;
    }
  }

  // If user enters /dns4|dns6|ip4|ip6/.../tcp/<port>/p2p/<peer>, insert /tls/ws before /p2p.
  if (value.includes("/tcp/")) {
    if (value.includes("/p2p/")) {
      const marker = "/p2p/";
      const idx = value.indexOf(marker);
      if (idx > 0) {
        return `${value.slice(0, idx)}/tls/ws${value.slice(idx)}`;
      }
    }
    return `${value}/tls/ws`;
  }

  return value;
}

function parseBundle(raw: string): AdminBundle | null {
  const value = raw.trim();
  if (!value) {
    return null;
  }

  let jsonText = value;
  if (value.startsWith("p2pos-admin://")) {
    const payload = value.slice("p2pos-admin://".length);
    try {
      jsonText = atob(payload);
    } catch {
      return null;
    }
  }

  try {
    const obj = JSON.parse(jsonText) as AdminBundle;
    if (
      !obj ||
      typeof obj.v !== "number" ||
      typeof obj.cluster_id !== "string" ||
      typeof obj.bootstrap !== "string" ||
      typeof obj.admin_priv !== "string" ||
      typeof obj.admin_proof !== "object"
    ) {
      return null;
    }
    return obj;
  } catch {
    return null;
  }
}

function formatUnknownError(err: unknown, fallback: string): string {
  if (err instanceof Error) {
    return err.message;
  }
  if (typeof err === "string" && err.trim() !== "") {
    return err;
  }
  try {
    const raw = JSON.stringify(err);
    if (raw && raw !== "{}") {
      return raw;
    }
  } catch {
    // ignore
  }
  return fallback;
}

async function resolveBootstrapCandidates(addr: string): Promise<string[]> {
  const value = addr.trim();
  if (value === "") {
    return [];
  }

  if (!value.startsWith("/dnsaddr/")) {
    if (isBrowserTransportAddr(value)) {
      return [value];
    }
    throw new Error("bootstrap address must be ws/wss/webtransport for browser libp2p");
  }

  const domain = value.slice("/dnsaddr/".length).trim();
  if (domain === "") {
    throw new Error("dnsaddr domain is empty");
  }

  const txtRecords = await lookupTXTCloudflare(`_dnsaddr.${domain}`);
  const out: string[] = [];
  const seen = new Set<string>();
  for (const record of txtRecords) {
    let v = record.trim();
    if (v.startsWith("dnsaddr=")) {
      v = v.slice("dnsaddr=".length).trim();
    }
    if (v === "") {
      continue;
    }
    const normalized = normalizeBootstrapAddr(v);
    if (!isBrowserTransportAddr(normalized)) {
      continue;
    }
    if (seen.has(normalized)) {
      continue;
    }
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

function isBrowserTransportAddr(addr: string): boolean {
  return addr.includes("/ws") || addr.includes("/wss") || addr.includes("/webtransport");
}

async function lookupTXTCloudflare(name: string): Promise<string[]> {
  const url = `https://cloudflare-dns.com/dns-query?name=${encodeURIComponent(name)}&type=TXT`;
  const res = await fetch(url, {
    headers: {
      accept: "application/dns-json"
    }
  });
  if (!res.ok) {
    throw new Error(`cloudflare doh http ${res.status}`);
  }
  const body = (await res.json()) as {
    Status?: number;
    Answer?: Array<{ type?: number; data?: string }>;
  };
  if ((body.Status ?? 0) !== 0) {
    throw new Error(`cloudflare doh dns status ${body.Status ?? -1}`);
  }
  const answers = body.Answer ?? [];
  const out: string[] = [];
  for (const ans of answers) {
    if (ans.type !== 16 || typeof ans.data !== "string") {
      continue;
    }
    let data = ans.data.trim();
    if (data.startsWith('"') && data.endsWith('"') && data.length >= 2) {
      data = data.slice(1, -1);
    }
    data = data.replaceAll('\\"', '"').trim();
    if (data !== "") {
      out.push(data);
    }
  }
  return out;
}
</script>
