package network

import "net"

type DNSResolver interface {
	LookupTXT(domain string) ([]string, error)
}

type NetDNSResolver struct{}

func NewNetDNSResolver() *NetDNSResolver {
	return &NetDNSResolver{}
}

func (r *NetDNSResolver) LookupTXT(domain string) ([]string, error) {
	return net.LookupTXT(domain)
}
