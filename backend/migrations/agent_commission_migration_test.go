package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration232GuardsEveryOneLevelWritePath(t *testing.T) {
	content, err := FS.ReadFile("232_agent_commission_system.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(content))
	require.Contains(t, sql, "for update")
	require.Contains(t, sql, "agent_profiles")
	require.Contains(t, sql, "create trigger agent_profiles_one_level_guard")
	require.Contains(t, sql, "create trigger agent_commissions_one_level_guard")
	require.Contains(t, sql, "agent commissions must reference a direct customer")
	require.Contains(t, sql, "agent user cannot also be a customer")
	require.Contains(t, sql, "target user is not an enabled agent")
}

func TestMigration233AllowsDualRoleAndKeepsDirectCommissionOnly(t *testing.T) {
	content, err := FS.ReadFile("233_allow_agent_customer_dual_role.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(content))
	require.Contains(t, sql, "create or replace function sub2api_enforce_agent_one_level")
	require.Contains(t, sql, "create or replace function sub2api_enforce_agent_profile_one_level")
	require.Contains(t, sql, "create or replace function sub2api_enforce_agent_commission_one_level")
	require.Contains(t, sql, "commission recording never follows that upstream edge")
	require.NotContains(t, sql, "agent user cannot be assigned as a customer")
	require.NotContains(t, sql, "agent user cannot also be a customer")
	require.NotContains(t, sql, "agent commissions cannot be generated for an agent customer")
	require.NotContains(t, sql, "nested agent relationships are not allowed")
}
