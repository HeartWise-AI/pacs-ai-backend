package entity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveAccountAccessStateBackfillsLegacyUsersSafely(t *testing.T) {
	require.Equal(t, AccountAccessActive, ResolveAccountAccessState("", false))
	require.Equal(t, AccountAccessSuspended, ResolveAccountAccessState("", true))
	require.Equal(t, AccountAccessSuspended, ResolveAccountAccessState(AccountAccessSuspended, false))
	require.Equal(t, AccountAccessSuspended, ResolveAccountAccessState(AccountAccessActive, true))
	require.Equal(t, AccountAccessSuspended, ResolveAccountAccessState("UNKNOWN", false))
}
