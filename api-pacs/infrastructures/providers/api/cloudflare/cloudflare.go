package cloudflare

import (
	"net/http"
	"time"
)

type CloudflareAPI struct {
	SecretKey string
}

var (
	Client *http.Client = &http.Client{Timeout: 30 * time.Second}
)

// Init initializes the cloudflare api
func Init(secretKey string) *CloudflareAPI {
	return &CloudflareAPI{
		SecretKey: secretKey,
	}
}
