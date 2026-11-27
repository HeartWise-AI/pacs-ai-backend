package service

import "testing"

// The release flow (scripts/release-model.py) publishes heartwisehub/<repo>:<semver>
// and the immutable heartwisehub/<repo>@sha256:<digest>; both stay registrable.
func TestRegistrationImageAllowlist(t *testing.T) {
	const digest = "sha256:b9b983e8f8028346dc316f5600fc307f83e6f543eab3761ef8c4a1b590505828"
	cases := []struct {
		image, namespaces, want string
		ok                      bool
	}{
		{"heartwisehub/pacs-ai-deepecg-clip-dinov3:1.4.0", "", "heartwisehub/pacs-ai-deepecg-clip-dinov3:1.4.0", true},
		{"docker.io/heartwisehub/deepcoro-jepa:2.2.1", "", "heartwisehub/deepcoro-jepa:2.2.1", true},
		{"heartwisehub/pacs-ai-deepecg-clip-dinov3@" + digest, "", "heartwisehub/pacs-ai-deepecg-clip-dinov3@" + digest, true},
		{"heartwisehub/pacs-ai-deepecg-clip-dinov3:1.4.0@" + digest, "", "heartwisehub/pacs-ai-deepecg-clip-dinov3:1.4.0@" + digest, true},
		{"heartwisehub/model:1.0.0-rc.1", "", "heartwisehub/model:1.0.0-rc.1", true},
		{"heartwisehub/model:latest", "", "", false},
		{"heartwisehub/model", "", "", false},
		{"attacker/implant:1.0.0", "", "", false},
		{"evil.example/heartwisehub/model:1.0.0", "", "", false},
		// configurable: a second namespace is accepted only when listed
		{"partnerlab/model:2.0.0", "heartwisehub, partnerlab", "partnerlab/model:2.0.0", true},
		{"heartwisehub/model:2.0.0", "heartwisehub,partnerlab", "heartwisehub/model:2.0.0", true},
		{"partnerlab/model:2.0.0", "heartwisehub", "", false},
		{"registry.example.org/team/model:3.1.4", "registry.example.org/team", "registry.example.org/team/model:3.1.4", true},
	}
	for _, c := range cases {
		got, err := normalizeRegistrationImage(c.image, c.namespaces)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("%q with %q: got %q, %v; want %q", c.image, c.namespaces, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("%q with %q: accepted as %q, want refused", c.image, c.namespaces, got)
		}
	}
}
