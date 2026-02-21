package membership

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
)

type AdminProof struct {
	ClusterID string    `json:"cluster_id"`
	PeerID    string    `json:"peer_id"`
	Role      string    `json:"role"`
	ValidFrom time.Time `json:"valid_from"`
	ValidTo   time.Time `json:"valid_to"`
	Sig       string    `json:"sig"`
}

type Snapshot struct {
	ClusterID    string     `json:"cluster_id"`
	IssuedAt     time.Time  `json:"issued_at"`
	IssuerPeerID string     `json:"issuer_peer_id"`
	Members      []string   `json:"members"`
	AdminProof   AdminProof `json:"admin_proof"`
	Sig          string     `json:"sig"`
}

type Manager struct {
	mu        sync.RWMutex
	clusterID string
	localPeer string
	systemPub crypto.PubKey
	hasPubKey bool
	snapshot  Snapshot
	memberSet map[string]struct{}
}

func NewManager(clusterID, systemPubKey, localPeerID string, initialMembers []string) (*Manager, error) {
	cluster := strings.TrimSpace(clusterID)
	if cluster == "" {
		cluster = "default"
	}

	m := &Manager{
		clusterID: cluster,
		localPeer: localPeerID,
		memberSet: make(map[string]struct{}),
	}

	if key := strings.TrimSpace(systemPubKey); key != "" {
		raw, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("decode system_pubkey failed: %w", err)
		}
		pub, err := crypto.UnmarshalPublicKey(raw)
		if err != nil {
			return nil, fmt.Errorf("unmarshal system_pubkey failed: %w", err)
		}
		m.systemPub = pub
		m.hasPubKey = true
	}

	normalized := normalizeMembers(initialMembers)

	m.snapshot = Snapshot{
		ClusterID: cluster,
		IssuedAt:  time.Time{},
		Members:   normalized,
	}
	for _, id := range normalized {
		m.memberSet[id] = struct{}{}
	}

	return m, nil
}

func (m *Manager) HasMembers() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.memberSet) > 0
}

func (m *Manager) IsMember(peerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.memberSet[peerID]
	return ok
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneSnapshot(m.snapshot)
}

func (m *Manager) Apply(snapshot Snapshot) error {
	snapshot.Members = normalizeMembers(snapshot.Members)
	if err := m.validateSnapshot(snapshot); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !snapshot.IssuedAt.UTC().After(m.snapshot.IssuedAt.UTC()) {
		return nil
	}

	m.snapshot = cloneSnapshot(snapshot)
	m.memberSet = make(map[string]struct{}, len(snapshot.Members))
	for _, id := range snapshot.Members {
		m.memberSet[id] = struct{}{}
	}
	return nil
}

func (m *Manager) validateSnapshot(snapshot Snapshot) error {
	if strings.TrimSpace(snapshot.ClusterID) != m.clusterID {
		return fmt.Errorf("cluster_id mismatch")
	}
	if snapshot.IssuedAt.IsZero() {
		return fmt.Errorf("issued_at is required")
	}
	if len(snapshot.Members) == 0 {
		return fmt.Errorf("members is empty")
	}
	if strings.TrimSpace(snapshot.IssuerPeerID) == "" {
		return fmt.Errorf("issuer_peer_id is required")
	}
	if strings.TrimSpace(snapshot.Sig) == "" {
		return fmt.Errorf("snapshot signature is required")
	}
	if m.hasPubKey {
		if err := m.validateAdminProof(snapshot.AdminProof, snapshot.IssuerPeerID); err != nil {
			return err
		}
	}

	if err := verifySnapshotSignature(snapshot); err != nil {
		return err
	}
	return nil
}

func (m *Manager) ValidateAdminProof(proof AdminProof, issuer string) error {
	if !m.hasPubKey {
		return fmt.Errorf("system_pubkey is required for admin proof validation")
	}
	return m.validateAdminProof(proof, issuer)
}

func (m *Manager) validateAdminProof(proof AdminProof, issuer string) error {
	if proof.Role != "admin" {
		return fmt.Errorf("admin proof role invalid")
	}
	if proof.ClusterID != m.clusterID {
		return fmt.Errorf("admin proof cluster mismatch")
	}
	if proof.PeerID != issuer {
		return fmt.Errorf("admin proof peer mismatch")
	}
	now := time.Now().UTC()
	if now.Before(proof.ValidFrom.UTC()) || now.After(proof.ValidTo.UTC()) {
		return fmt.Errorf("admin proof expired or not yet valid")
	}

	sigBytes, err := base64.StdEncoding.DecodeString(proof.Sig)
	if err != nil {
		return fmt.Errorf("decode admin proof sig failed: %w", err)
	}
	ok, err := m.systemPub.Verify(canonicalAdminProof(proof), sigBytes)
	if err != nil {
		return fmt.Errorf("verify admin proof failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("admin proof signature invalid")
	}
	return nil
}

func SignSnapshot(priv crypto.PrivKey, snapshot Snapshot) (Snapshot, error) {
	if priv == nil {
		return snapshot, fmt.Errorf("private key is nil")
	}
	if strings.TrimSpace(snapshot.ClusterID) == "" {
		return snapshot, fmt.Errorf("cluster_id is required")
	}
	if strings.TrimSpace(snapshot.IssuerPeerID) == "" {
		return snapshot, fmt.Errorf("issuer_peer_id is required")
	}
	if snapshot.IssuedAt.IsZero() {
		return snapshot, fmt.Errorf("issued_at is required")
	}
	snapshot.Members = normalizeMembers(snapshot.Members)
	if len(snapshot.Members) == 0 {
		return snapshot, fmt.Errorf("members is empty")
	}

	sig, err := priv.Sign(canonicalSnapshot(snapshot))
	if err != nil {
		return snapshot, err
	}
	snapshot.Sig = base64.StdEncoding.EncodeToString(sig)
	return snapshot, nil
}

func verifySnapshotSignature(snapshot Snapshot) error {
	id, err := peerstore.Decode(snapshot.IssuerPeerID)
	if err != nil {
		return fmt.Errorf("decode issuer peer id failed: %w", err)
	}
	pub, err := id.ExtractPublicKey()
	if err != nil {
		return fmt.Errorf("extract issuer public key failed: %w", err)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(snapshot.Sig)
	if err != nil {
		return fmt.Errorf("decode snapshot sig failed: %w", err)
	}

	ok, err := pub.Verify(canonicalSnapshot(snapshot), sigBytes)
	if err != nil {
		return fmt.Errorf("verify snapshot signature failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("snapshot signature invalid")
	}
	return nil
}

func normalizeMembers(in []string) []string {
	uniq := make(map[string]struct{}, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		uniq[id] = struct{}{}
	}
	out := make([]string, 0, len(uniq))
	for id := range uniq {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func cloneSnapshot(s Snapshot) Snapshot {
	out := s
	out.Members = append([]string(nil), s.Members...)
	return out
}

func canonicalAdminProof(p AdminProof) []byte {
	return []byte(strings.Join([]string{
		p.ClusterID,
		p.PeerID,
		p.Role,
		p.ValidFrom.UTC().Format(time.RFC3339Nano),
		p.ValidTo.UTC().Format(time.RFC3339Nano),
	}, "|"))
}

func canonicalSnapshot(s Snapshot) []byte {
	members := normalizeMembers(s.Members)
	return []byte(strings.Join([]string{
		s.ClusterID,
		s.IssuedAt.UTC().Format(time.RFC3339Nano),
		s.IssuerPeerID,
		strings.Join(members, ","),
	}, "|"))
}
