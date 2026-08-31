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
