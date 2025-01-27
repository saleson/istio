// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package network

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"istio.io/istio/pkg/sleep"
	"istio.io/pkg/log"
)

// Network-related utility functions
const (
	waitInterval = 100 * time.Millisecond
	waitTimeout  = 2 * time.Minute
)

type lookupIPAddrType = func(ctx context.Context, addr string) ([]netip.Addr, error)

// ErrResolveNoAddress error occurs when IP address resolution is attempted,
// but no address was provided.
var ErrResolveNoAddress = fmt.Errorf("no address specified")

// GetPrivateIPs blocks until private IP addresses are available, or a timeout is reached.
func GetPrivateIPs(ctx context.Context) ([]string, bool) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, waitTimeout)
		defer cancel()
	}

	for {
		select {
		case <-ctx.Done():
			return getPrivateIPsIfAvailable()
		default:
			addr, ok := getPrivateIPsIfAvailable()
			if ok {
				return addr, true
			}
			sleep.UntilContext(ctx, waitInterval)
		}
	}
}

// Returns all the private IP addresses
func getPrivateIPsIfAvailable() ([]string, bool) {
	ok := true
	ipAddresses := make([]string, 0)

	ifaces, _ := net.Interfaces()

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		addrs, _ := iface.Addrs()

		for _, addr := range addrs {
			var ipAddr netip.Addr
			addrType := addr.Network()
			if addrType == "ip+net" {
				ipNet, iErr := netip.ParsePrefix(addr.String())
				if iErr != nil {
					continue
				}
				ipAddr = ipNet.Addr()
			} else if addrType == "ip" {
				ip, aErr := netip.ParseAddr(addr.String())
				if aErr != nil {
					continue
				}
				ipAddr = ip
			}
			// unwrap the IPv4-mapped IPv6 address
			unwrapAddr := ipAddr.Unmap()
			if !unwrapAddr.IsValid() || unwrapAddr.IsLoopback() || unwrapAddr.IsLinkLocalUnicast() || unwrapAddr.IsLinkLocalMulticast() {
				continue
			}
			if unwrapAddr.IsUnspecified() {
				ok = false
				continue
			}
			ipAddresses = append(ipAddresses, unwrapAddr.String())
		}
	}
	return ipAddresses, ok
}

// ResolveAddr resolves an authority address to an IP address. Incoming
// addr can be an IP address or hostname. If addr is an IPv6 address, the IP
// part must be enclosed in square brackets.
//
// LookupIPAddr() may return multiple IP addresses, of which this function returns
// the first IPv4 entry. To use this function in an IPv6 only environment, either
// provide an IPv6 address or ensure the hostname resolves to only IPv6 addresses.
func ResolveAddr(addr string, lookupIPAddr ...lookupIPAddrType) (string, error) {
	if addr == "" {
		return "", ErrResolveNoAddress
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}

	log.Infof("Attempting to lookup address: %s", host)
	defer log.Infof("Finished lookup of address: %s", host)
	// lookup the udp address with a timeout of 15 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var addrs []netip.Addr
	var lookupErr error
	if (len(lookupIPAddr) > 0) && (lookupIPAddr[0] != nil) {
		// if there are more than one lookup function, ignore all but first
		addrs, lookupErr = lookupIPAddr[0](ctx, host)
	} else {
		addrs, lookupErr = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	if lookupErr != nil || len(addrs) == 0 {
		return "", fmt.Errorf("lookup failed for IP address: %w", lookupErr)
	}
	var resolvedAddr string

	for _, addr := range addrs {
		// unwrap the IPv4-mapped IPv6 address
		unwrapAddr := addr.Unmap()
		if !unwrapAddr.IsValid() {
			continue
		}
		pPort, pErr := strconv.ParseUint(port, 10, 16)
		if pErr != nil {
			continue
		}
		tmpAddPort := netip.AddrPortFrom(unwrapAddr, uint16(pPort))
		resolvedAddr = tmpAddPort.String()
		if unwrapAddr.Is4() {
			break
		}
	}
	log.Infof("Addr resolved to: %s", resolvedAddr)
	return resolvedAddr, nil
}

// AllIPv6 checks the addresses slice and returns true if all addresses
// are valid IPv6 address, for all other cases it returns false.
func AllIPv6(ipAddrs []string) bool {
	for i := 0; i < len(ipAddrs); i++ {
		addr, err := netip.ParseAddr(ipAddrs[i])
		if err != nil {
			// Should not happen, invalid IP in proxy's IPAddresses slice should have been caught earlier,
			// skip it to prevent a panic.
			continue
		}
		if addr.Is4() {
			return false
		}
	}
	return true
}

// AllIPv4 checks the addresses slice and returns true if all addresses
// are valid IPv4 address, for all other cases it returns false.
func AllIPv4(ipAddrs []string) bool {
	for i := 0; i < len(ipAddrs); i++ {
		addr, err := netip.ParseAddr(ipAddrs[i])
		if err != nil {
			// Should not happen, invalid IP in proxy's IPAddresses slice should have been caught earlier,
			// skip it to prevent a panic.
			continue
		}
		if !addr.Is4() && addr.Is6() {
			return false
		}
	}
	return true
}

// GlobalUnicastIP returns the first global unicast address in the passed in addresses.
func GlobalUnicastIP(ipAddrs []string) string {
	for i := 0; i < len(ipAddrs); i++ {
		addr, err := netip.ParseAddr(ipAddrs[i])
		if err != nil {
			// Should not happen, invalid IP in proxy's IPAddresses slice should have been caught earlier,
			// skip it to prevent a panic.
			continue
		}
		if addr.IsGlobalUnicast() {
			return addr.String()
		}
	}
	return ""
}
