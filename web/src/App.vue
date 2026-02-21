<template>
  <div class="page">
    <div>
      <div class="title">P2POS Admin</div>
      <div class="subtitle">Cloudflare Worker + Web UI (browser libp2p client)</div>
    </div>

    <div class="grid">
      <section class="card">
        <h2>Connection</h2>
        <label>Bootstrap Multiaddr</label>
        <input v-model="bootstrapAddr" placeholder="/dns4/init.p2pos.zhongwwwhhh.cc/tcp/4100/p2p/..." />
        <div class="hint">Auto normalized: {{ normalizedBootstrapAddr || "-" }}</div>

        <label>Cluster ID</label>
        <input v-model="clusterId" placeholder="default" />

        <div class="row">
          <button class="btn" :disabled="!canConnect" @click="connectNode">Connect</button>
          <button class="btn secondary" :disabled="!client" @click="disconnectNode">Disconnect</button>
        </div>

        <div class="status" style="margin-top: 12px;">
          <span class="dot"></span>
          <span>State: {{ runtimeState }}</span>
        </div>
        <div v-if="connectError" class="error">{{ connectError }}</div>
        <div class="hint">浏览器仅支持 WebSocket/WebTransport，服务端需支持对应传输。</div>
      </section>

      <section class="card">
        <h2>Admin Credentials</h2>
        <label>Admin Node Private Key (base64)</label>
        <textarea v-model="adminPrivKey" placeholder="Paste admin node private key"></textarea>

        <label>Admin Proof (JSON)</label>
        <textarea v-model="adminProofJson" placeholder='{"cluster_id":"...","peer_id":"...","role":"admin","valid_from":"...","valid_to":"...","sig":"..."}'></textarea>

        <div class="row">
          <button class="btn" :disabled="!canLoadCreds" @click="loadCreds">Load Credentials</button>
          <button class="btn secondary" @click="clearCreds">Clear</button>
        </div>

        <div v-if="credError" class="error">{{ credError }}</div>
        <div v-if="adminState" class="hint">Admin loaded: {{ adminState }}</div>
      </section>

      <section class="card">
        <h2>Membership List</h2>
        <div class="list">
          <div v-for="id in members" :key="id" class="list-item">
            <span>{{ id }}</span>
            <button class="btn secondary" @click="removeMember(id)">Remove</button>
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
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import { connect, createClient, pushMembershipSnapshot, type Libp2pClient } from "./libp2p";

const bootstrapAddr = ref("/dns4/init.p2pos.zhongwwwhhh.cc/tcp/4100/p2p/");
const clusterId = ref("default");
const adminPrivKey = ref("");
const adminProofJson = ref("");
const members = ref<string[]>([]);
const newMember = ref("");
const runtimeState = ref("unconfigured");
const credError = ref("");
const adminState = ref("");
const issuedAt = ref(new Date().toISOString());
const client = ref<Libp2pClient | null>(null);
const connectError = ref("");
const connectedAddr = ref("");

type AdminProof = {
  cluster_id: string;
  peer_id: string;
  role: string;
  valid_from: string;
  valid_to: string;
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

const canLoadCreds = computed(() => {
  return adminPrivKey.value.trim().length > 0 && adminProofJson.value.trim().length > 0;
});

const canPublish = computed(() => {
  return members.value.length > 0 && adminState.value !== "" && client.value !== null;
});

const loadCreds = () => {
  credError.value = "";
  adminState.value = "";
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

const clearCreds = () => {
  adminPrivKey.value = "";
  adminProofJson.value = "";
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
    if (!addr.includes("/ws") && !addr.includes("/wss") && !addr.includes("/webtransport")) {
      throw new Error("bootstrap address must be ws/wss/webtransport for browser libp2p");
    }
    const node = await createClient(adminPrivKey.value.trim());
    await connect(node, addr);
    client.value = node;
    connectedAddr.value = addr;
    bootstrapAddr.value = addr;
    runtimeState.value = "connected";
  } catch (err) {
    connectError.value = err instanceof Error ? err.message : "connect failed";
  }
};

const disconnectNode = async () => {
  if (!client.value) return;
  await client.value.stop();
  client.value = null;
  connectedAddr.value = "";
  runtimeState.value = "unconfigured";
};

const publishSnapshot = async () => {
  if (!client.value) return;
  try {
    const addr = connectedAddr.value || normalizedBootstrapAddr.value;
    if (!addr) {
      throw new Error("bootstrap address is empty");
    }
    await pushMembershipSnapshot(client.value, addr, snapshotJson.value);
    runtimeState.value = "healthy";
  } catch (err) {
    connectError.value = err instanceof Error ? err.message : "publish failed";
  }
};

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
</script>
