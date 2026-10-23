package net

import (
	"context"
	"errors"
)

// LookupIP looks up host for the given network using the local resolver.
// The network must be one of "ip", "ip4" or "ip6".
func (r *Resolver) LookupIP(ctx context.Context, network, host string) ([]IP, error) {
	switch network {
	case "ip", "ip4", "ip6":
	default:
		return nil, UnknownNetworkError(network)
	}
	addrs, err := r.LookupNetIP(ctx, network, host)
	if err != nil {
		return nil, err
	}
	ips := make([]IP, 0, len(addrs))
	for _, a := range addrs {
		if network == "ip4" && !a.Is4() || network == "ip6" && a.Is4() {
			continue
		}
		ips = append(ips, IP(a.AsSlice()))
	}
	if len(ips) == 0 {
		return nil, &DNSError{Err: errNoSuchHost.Error(), Name: host, IsNotFound: true}
	}
	return ips, nil
}

// An MX represents a single DNS MX record.
type MX struct {
	Host string
	Pref uint16
}

// An NS represents a single DNS NS record.
type NS struct {
	Host string
}

// LookupCNAME returns the canonical name for the given host.
//
// TINYGO: not implemented; netdev provides no CNAME lookup.
func (r *Resolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return "", errors.New("net:LookupCNAME not implemented")
}

// LookupMX returns the DNS MX records for the given domain name.
//
// TINYGO: not implemented; netdev provides no MX record lookup.
func (r *Resolver) LookupMX(ctx context.Context, name string) ([]*MX, error) {
	return nil, errors.New("net:LookupMX not implemented")
}

// LookupNS returns the DNS NS records for the given domain name.
//
// TINYGO: not implemented; netdev provides no NS record lookup.
func (r *Resolver) LookupNS(ctx context.Context, name string) ([]*NS, error) {
	return nil, errors.New("net:LookupNS not implemented")
}

// LookupAddr performs a reverse lookup for the given address.
//
// TINYGO: not implemented; netdev provides no reverse lookup.
func (r *Resolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return nil, errors.New("net:LookupAddr not implemented")
}

// LookupCNAME returns the canonical name for the given host.
func LookupCNAME(host string) (string, error) {
	return DefaultResolver.LookupCNAME(context.Background(), host)
}

// LookupSRV tries to resolve an SRV query of the given service, protocol, and
// domain name.
func LookupSRV(service, proto, name string) (string, []*SRV, error) {
	return DefaultResolver.LookupSRV(context.Background(), service, proto, name)
}

// LookupMX returns the DNS MX records for the given domain name.
func LookupMX(name string) ([]*MX, error) {
	return DefaultResolver.LookupMX(context.Background(), name)
}

// LookupNS returns the DNS NS records for the given domain name.
func LookupNS(name string) ([]*NS, error) {
	return DefaultResolver.LookupNS(context.Background(), name)
}

// LookupTXT returns the DNS TXT records for the given domain name.
func LookupTXT(name string) ([]string, error) {
	return DefaultResolver.LookupTXT(context.Background(), name)
}

// LookupAddr performs a reverse lookup for the given address.
func LookupAddr(addr string) ([]string, error) {
	return DefaultResolver.LookupAddr(context.Background(), addr)
}
