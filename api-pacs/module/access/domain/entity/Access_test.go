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

func TestResearchModelsAreOutsideEveryModelAccessOfUsers(t *testing.T) {
	t.Setenv("PACS_RESEARCH_MODEL_IDS", "EchoAlign")
	base := ForRole(iamEntity.UserRole, nil)
	require.True(t, base.AllModels)
	require.False(t, base.Unrestricted())
	require.False(t, base.AllowsModel("EchoAlign"))
	require.True(t, base.AllowsModel("PanEcho"))
	require.True(t, base.AllowsModel(""), "an unknown model is not a research model")

	allModelsRole := ForRole(iamEntity.UserRole, &CustomRole{ID: "r", Name: "Clinicians", AllModels: true})
	require.False(t, allModelsRole.AllowsModel("EchoAlign"))
	require.True(t, allModelsRole.AllowsModel("PanEcho"))

	research := ForRole(iamEntity.UserRole, &CustomRole{ID: "r2", Name: "Research", ModelIDs: []string{"EchoAlign", "PanEcho"}})
	require.True(t, research.AllowsModel("EchoAlign"))
	require.False(t, research.AllowsModel("CathEF_CLIP"))
	require.False(t, research.AllowsModel(""))

	clinicianList := ForRole(iamEntity.UserRole, &CustomRole{ID: "r3", ModelIDs: []string{"PanEcho"}})
	require.False(t, clinicianList.AllowsModel("EchoAlign"))

	for _, role := range []string{iamEntity.OwnerRole, iamEntity.AdminRole} {
		access := ForRole(role, nil)
		require.True(t, access.Unrestricted(), role)
		require.True(t, access.AllowsModel("EchoAlign"), role)
	}
}

func TestResearchModelListComesFromTheEnvironment(t *testing.T) {
	t.Setenv("PACS_RESEARCH_MODEL_IDS", " A , B ,")
	require.Equal(t, []string{"A", "B"}, ResearchModelIDs())
	require.True(t, IsResearchModel("B"))
	require.False(t, ForRole(iamEntity.UserRole, nil).AllowsModel("A"))
	require.True(t, ForRole(iamEntity.UserRole, nil).AllowsModel("EchoAlign"))

	t.Setenv("PACS_RESEARCH_MODEL_IDS", "")
	require.Empty(t, ResearchModelIDs())
	require.True(t, ForRole(iamEntity.UserRole, nil).Unrestricted(), "an empty list disables the exclusion")
}

func TestDefaultResearchModelList(t *testing.T) {
	require.Equal(t, []string{"EchoAlign"}, DefaultResearchModelIDs)
}
