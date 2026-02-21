package network

import "p2pos/internal/membership"

func (n *Node) notifyMembershipApplied(snapshot membership.Snapshot) {
	n.memberMu.RLock()
	fn := n.onMembershipApplied
	n.memberMu.RUnlock()
	if fn == nil {
		return
	}
	fn(snapshot)
}
