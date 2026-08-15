package entity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserPolicyAcceptanceDocumentIDIsStableAndVersioned(t *testing.T) {
	acceptance := UserPolicyAcceptance{
		TenantID: "tenant-a", UserID: "user-a", PolicyKey: PolicyTermsOfService, Version: "2026-08-15",
	}

	require.Equal(t, acceptance.DocumentID(), acceptance.DocumentID())
	require.Len(t, acceptance.DocumentID(), 64)

	updated := acceptance
	updated.Version = "2026-09-01"
	require.NotEqual(t, acceptance.DocumentID(), updated.DocumentID())
}

func TestUserPolicyAcceptanceDocumentIDIncludesTenantScope(t *testing.T) {
	acceptance := UserPolicyAcceptance{
		TenantID: "tenant-a", UserID: "shared-user", PolicyKey: PolicyPrivacyPolicy, Version: "2026-08-15",
	}
	otherTenant := acceptance
	otherTenant.TenantID = "tenant-b"

	require.NotEqual(t, acceptance.DocumentID(), otherTenant.DocumentID())
}
