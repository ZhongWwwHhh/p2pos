package network

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"p2pos/internal/logging"
	"p2pos/internal/status"

	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const statusProtocolID = protocol.ID("/p2pos/status/1.0.0")

type statusScope string

const (
	statusScopeLocal   statusScope = "local"
	statusScopeCluster statusScope = "cluster"
)

type statusRequest struct {
	Scope statusScope `json:"scope,omitempty"`
}

type statusResponse struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Peers       []status.Record `json:"peers"`
	Error       string          `json:"error,omitempty"`
}

func (n *Node) registerStatusHandler() {
	n.Host.SetStreamHandler(statusProtocolID, func(stream libp2pnet.Stream) {
		defer stream.Close()

		req := statusRequest{Scope: statusScopeLocal}
		_ = json.NewDecoder(stream).Decode(&req)
		if req.Scope == "" {
			req.Scope = statusScopeLocal
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp := statusResponse{
			GeneratedAt: time.Now().UTC(),
			Peers:       []status.Record{},
		}
		if !n.canUseBusinessProtocols() {
			resp.Error = "node is unconfigured"
			logging.Log("STATUS", "deny_unconfigured", map[string]string{
				"peer_id": n.Host.ID().String(),
			})
			_ = json.NewEncoder(stream).Encode(resp)
			return
		}

		var (
			peers []status.Record
			err   error
		)
		if req.Scope == statusScopeCluster {
			peers, err = n.ClusterStatus(ctx)
		} else {
			peers, err = n.localStatus(ctx)
		}
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Peers = peers
		}

		if err := json.NewEncoder(stream).Encode(resp); err != nil {
			logging.Log("STATUS", "encode_failed", map[string]string{
				"reason": err.Error(),
			})
		}
	})
}

func (n *Node) localStatus(ctx context.Context) ([]status.Record, error) {
	n.statusMu.RLock()
	provider := n.status
	n.statusMu.RUnlock()
	if provider == nil {
		return []status.Record{}, nil
	}
	return provider.Snapshot(ctx)
}

func (n *Node) FetchStatus(ctx context.Context, peerID peerstore.ID, scope string) ([]status.Record, error) {
	stream, err := n.Host.NewStream(ctx, peerID, statusProtocolID)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	req := statusRequest{Scope: statusScope(scope)}
	if req.Scope == "" {
		req.Scope = statusScopeLocal
	}
	if req.Scope != statusScopeCluster {
		req.Scope = statusScopeLocal
	}

	if err := json.NewEncoder(stream).Encode(req); err != nil {
		return nil, err
	}

	var resp statusResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Peers, nil
}

func (n *Node) ClusterStatus(ctx context.Context) ([]status.Record, error) {
	if !n.canUseBusinessProtocols() {
		return nil, errors.New("node is unconfigured")
	}
	all := make([]status.Record, 0)

	local, err := n.localStatus(ctx)
	if err != nil {
		return nil, err
	}
	all = append(all, local...)

	for _, peerID := range n.Host.Network().Peers() {
		if _, skip := n.statusUnsupported.Load(peerID); skip {
			continue
		}
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		remote, err := n.FetchStatus(reqCtx, peerID, string(statusScopeLocal))
		cancel()
		if err != nil {
			if strings.Contains(err.Error(), "protocols not supported") {
				n.statusUnsupported.Store(peerID, struct{}{})
				logging.Log("STATUS", "skip_unsupported_peer", map[string]string{
					"peer_id": peerID.String(),
				})
				continue
			}
			logging.Log("STATUS", "query_failed", map[string]string{
				"peer_id": peerID.String(),
				"reason":  err.Error(),
			})
			continue
		}
		all = append(all, remote...)
	}

	return mergeStatusRecords(all), nil
}

func mergeStatusRecords(in []status.Record) []status.Record {
	merged := make(map[string]status.Record)
	for _, rec := range in {
		if rec.PeerID == "" {
			continue
		}
		prev, ok := merged[rec.PeerID]
		if !ok || recordIsNewer(rec, prev) {
			merged[rec.PeerID] = rec
		}
	}

	out := make([]status.Record, 0, len(merged))
	for _, rec := range merged {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

func recordIsNewer(a, b status.Record) bool {
	return recordTimestamp(a).After(recordTimestamp(b))
}

func recordTimestamp(r status.Record) time.Time {
	return r.LastSeenAt.UTC()
}
