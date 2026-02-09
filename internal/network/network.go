package network

import (
	"context"
	"p2pos/internal/sqlite"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
)

// InitNetworkConnect 初始化libp2p网络连接
func InitNetworkConnect(h host.Host) error {
	initConn, err := sqlite.GetInitConnections()
	if err != nil {
		return err
	}

	for _, conn := range initConn {
		addrStr := conn.Address
		if conn.Type == "dns" && !hasDNSPrefix(addrStr) {
			addrStr = "/dnsaddr/" + addrStr
		}
		maddr, err := multiaddr.NewMultiaddr(addrStr)
		if err != nil {
			continue
		}
		pi, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			continue
		}
		// 加入peerstore，便于后续连接和流创建
		h.Peerstore().AddAddrs(pi.ID, pi.Addrs, peerstore.PermanentAddrTTL)
		// 尝试连接
		go h.Connect(context.Background(), *pi)
	}

	return nil
}

// 判断地址是否已带/dnsaddr/前缀
func hasDNSPrefix(addr string) bool {
	return len(addr) >= 9 && addr[:9] == "/dnsaddr/"
}
