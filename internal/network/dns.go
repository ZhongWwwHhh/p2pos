package network

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DNSResolver interface {
	LookupTXT(domain string) ([]string, error)
}

type NetDNSResolver struct{}

func NewNetDNSResolver() *NetDNSResolver {
	return &NetDNSResolver{}
}

func (r *NetDNSResolver) LookupTXT(domain string) ([]string, error) {
	name := strings.TrimSpace(domain)
	if name == "" {
		return nil, fmt.Errorf("empty domain")
	}

	// Prefer Cloudflare DoH to reduce stale TXT results from other recursive resolvers.
	if records, err := lookupTXTFromCloudflare(name); err == nil && len(records) > 0 {
		return records, nil
	}

	// Fallback to system resolver.
	return net.LookupTXT(name)
}

type dohResponse struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func lookupTXTFromCloudflare(name string) ([]string, error) {
	endpoint := "https://cloudflare-dns.com/dns-query?name=" + url.QueryEscape(name) + "&type=TXT"

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/dns-json")

	client := &http.Client{
		Timeout: 4 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cloudflare doh status %d", resp.StatusCode)
	}

	var body dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != 0 {
		return nil, fmt.Errorf("cloudflare doh dns status %d", body.Status)
	}

	out := make([]string, 0, len(body.Answer))
	for _, ans := range body.Answer {
		if ans.Type != 16 {
			continue
		}
		value := strings.TrimSpace(ans.Data)
		value = strings.TrimPrefix(value, "\"")
		value = strings.TrimSuffix(value, "\"")
		value = strings.ReplaceAll(value, "\\\"", "\"")
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out, nil
}
