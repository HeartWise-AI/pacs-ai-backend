package rest

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ParseTrustedProxyCIDRs parses the direct proxy networks allowed to supply
// X-Real-IP. An empty value trusts no proxy headers.
func ParseTrustedProxyCIDRs(value string) ([]*net.IPNet, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	var networks []*net.IPNet
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", entry, err)
		}
		networks = append(networks, network)
	}

	return networks, nil
}

func (controller *UserCommandController) registrationClientIP(request *http.Request) string {
	remoteIP := parseRemoteIP(request.RemoteAddr)
	if remoteIP == nil {
		return "unknown"
	}

	if isIPInNetworks(remoteIP, controller.TrustedProxyCIDRs) {
		forwardedIP := net.ParseIP(strings.TrimSpace(request.Header.Get("X-Real-IP")))
		if forwardedIP != nil {
			return forwardedIP.String()
		}
	}

	return remoteIP.String()
}

func parseRemoteIP(remoteAddress string) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(remoteAddress), "[]")
	}
	return net.ParseIP(host)
}

func isIPInNetworks(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
