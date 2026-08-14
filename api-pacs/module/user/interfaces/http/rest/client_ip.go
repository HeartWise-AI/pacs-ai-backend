package rest

import (
	"net"
	"net/http"

	"api-pacs/interfaces/http/rest/clientip"
)

// ParseTrustedProxyCIDRs parses the direct proxy networks allowed to supply
// X-Real-IP. An empty value trusts no proxy headers.
func ParseTrustedProxyCIDRs(value string) ([]*net.IPNet, error) {
	return clientip.ParseTrustedProxyCIDRs(value)
}

func (controller *UserCommandController) registrationClientIP(request *http.Request) string {
	return clientip.Resolve(request, controller.TrustedProxyCIDRs)
}
