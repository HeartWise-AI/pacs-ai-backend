package entity

import (
	"testing"

	"github.com/stretchr/testify/require"

	iamEntity "api-pacs/module/iam/domain/entity"
)

func TestOwnerAndAdminHoldEveryPermissionAndModel(t *testing.T) {
	for _, role := range []string{iamEntity.OwnerRole, iamEntity.AdminRole} {
		access := ForRole(role, &CustomRole{AllModels: false, ModelIDs: []string{"X"}})
		require.Equal(t, AllPermissions(), access.PermissionList(), role)
		require.True(t, access.AllModels, role)
		require.True(t, access.AllowsModel("anything"), role)
		require.Nil(t, access.CustomRole, "custom roles never apply to %s", role)
	}
}

func TestUserWithoutCustomRoleHoldsBaselineAndAllModels(t *testing.T) {
	access := ForRole(iamEntity.UserRole, nil)
	require.Equal(t, []string{PermModelsUse, PermFeedbackVote}, access.PermissionList())
	require.True(t, access.AllModels)
	require.False(t, access.Has(PermMembersInvite))
	require.Empty(t, access.ModelIDList())
}

func TestUserCustomRoleAddsGrantablePermissionsAndRestrictsModels(t *testing.T) {
	access := ForRole(iamEntity.UserRole, &CustomRole{
		ID: "r1", Name: "Readers",
		Permissions: []string{PermMembersInvite, "admin.everything", PermFeedbackView},
		ModelIDs:    []string{"DeepRV_CLIP", " PanEcho ", ""},
	})
	require.Equal(t, []string{PermModelsUse, PermFeedbackVote, PermFeedbackView, PermMembersInvite}, access.PermissionList())
	require.False(t, access.AllModels)
	require.True(t, access.AllowsModel("DeepRV_CLIP"))
	require.True(t, access.AllowsModel("PanEcho"))
	require.False(t, access.AllowsModel("CathEF_CLIP"))
	require.Equal(t, []string{"DeepRV_CLIP", "PanEcho"}, access.ModelIDList())
	require.Equal(t, &CustomRoleRef{ID: "r1", Name: "Readers"}, access.CustomRole)
}

func TestCustomRoleWithAllModelsKeepsEveryModel(t *testing.T) {
	access := ForRole(iamEntity.UserRole, &CustomRole{AllModels: true, ModelIDs: []string{"ignored"}})
	require.True(t, access.AllowsModel("CathEF_CLIP"))
	require.Empty(t, access.ModelIDList())
}

func TestUnknownRoleHoldsNothing(t *testing.T) {
	access := ForRole("GUEST", &CustomRole{Permissions: []string{PermLogsView}, AllModels: true})
	require.Empty(t, access.PermissionList())
	require.False(t, access.AllowsModel("PanEcho"))
	var zero Access
	require.False(t, zero.Has(PermModelsUse))
	require.False(t, zero.AllowsModel("PanEcho"))
}

func TestCatalogPartitions(t *testing.T) {
	for _, permission := range BaselinePermissions {
		require.True(t, IsBaselinePermission(permission))
		require.False(t, IsGrantablePermission(permission))
	}
	for _, permission := range GrantablePermissions {
		require.True(t, IsGrantablePermission(permission))
	}
	require.Len(t, AllPermissions(), 11)
}
