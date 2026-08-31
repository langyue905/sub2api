package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type agentPolicyRepoStub struct {
	assignCalls int
	assignErr   error
}

func (r *agentPolicyRepoStub) GetProfile(context.Context, int64) (*AgentProfile, error) {
	return nil, ErrAgentProfileNotFound
}
func (r *agentPolicyRepoStub) UpsertProfile(context.Context, int64, bool, int) (*AgentProfile, error) {
	return nil, nil
}
func (r *agentPolicyRepoStub) AssignCustomer(context.Context, int64, *int64) error {
	r.assignCalls++
	return r.assignErr
}
func (r *agentPolicyRepoStub) GetSummary(context.Context, int64) (*AgentSummary, error) {
	return nil, nil
}
func (r *agentPolicyRepoStub) ListCustomers(context.Context, int64, pagination.PaginationParams) ([]AgentCustomer, int64, error) {
	return nil, 0, nil
}
func (r *agentPolicyRepoStub) ListCommissions(context.Context, int64, pagination.PaginationParams) ([]AgentCommission, int64, error) {
	return nil, 0, nil
}
func (r *agentPolicyRepoStub) ListWithdrawals(context.Context, int64, pagination.PaginationParams) ([]AgentWithdrawal, int64, error) {
	return nil, 0, nil
}
func (r *agentPolicyRepoStub) CreateWithdrawal(context.Context, int64, float64, string, string, string) (*AgentWithdrawal, error) {
	return nil, nil
}
func (r *agentPolicyRepoStub) TransferCommission(context.Context, int64) (float64, error) {
	return 0, nil
}
func (r *agentPolicyRepoStub) ListProfiles(context.Context, AgentProfileFilter) ([]AgentProfileView, int64, error) {
	return nil, 0, nil
}
func (r *agentPolicyRepoStub) ListAdminWithdrawals(context.Context, AgentWithdrawalFilter) ([]AgentWithdrawal, int64, error) {
	return nil, 0, nil
}
func (r *agentPolicyRepoStub) ProcessWithdrawal(context.Context, int64, int64, string, string) (*AgentWithdrawal, error) {
	return nil, nil
}
func (r *agentPolicyRepoStub) RecordCommissionForUsage(context.Context, *UsageLog) error {
	return nil
}

func TestAgentRateTiersAndCommissionMath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		usage float64
		want  int
	}{
		{0, AgentDefaultRateBPS},
		{99.999999, AgentDefaultRateBPS},
		{100, AgentTierTwoRateBPS},
		{9999.99, AgentTierTwoRateBPS},
		{10000, AgentTierThreeRateBPS},
	}
	for _, tt := range tests {
		if got := ResolveAgentRateBPS(0, tt.usage); got != tt.want {
			t.Fatalf("ResolveAgentRateBPS(%v)=%d, want %d", tt.usage, got, tt.want)
		}
	}
	if got := ResolveAgentRateBPS(AgentTierThreeRateBPS, 0); got != AgentTierThreeRateBPS {
		t.Fatalf("manual rate was not preserved: %d", got)
	}
	if got := AgentCommissionAmount(12.34567891, AgentDefaultRateBPS); got != 0.86419752 {
		t.Fatalf("commission rounding got %v, want 0.86419752", got)
	}
}

func TestAffiliateServiceRejectsSelfAgentAssignmentBeforeRepository(t *testing.T) {
	t.Parallel()
	repo := &agentPolicyRepoStub{}
	svc := &AffiliateService{agentRepo: repo}
	agentID := int64(42)
	err := svc.AdminAssignAgentCustomer(context.Background(), agentID, &agentID)
	if !errors.Is(err, ErrAgentSelfAssignment) {
		t.Fatalf("self assignment error = %v, want %v", err, ErrAgentSelfAssignment)
	}
	if repo.assignCalls != 0 {
		t.Fatalf("repository was called for self assignment (%d calls)", repo.assignCalls)
	}
}

func TestAffiliateServicePropagatesOneLevelAssignmentGuard(t *testing.T) {
	t.Parallel()
	repo := &agentPolicyRepoStub{assignErr: ErrAgentNested}
	svc := &AffiliateService{agentRepo: repo}
	agentID := int64(7)
	if err := svc.AdminAssignAgentCustomer(context.Background(), 8, &agentID); !errors.Is(err, ErrAgentNested) {
		t.Fatalf("nested assignment error = %v, want %v", err, ErrAgentNested)
	}
	if repo.assignCalls != 1 {
		t.Fatalf("expected one repository assignment call, got %d", repo.assignCalls)
	}
}

func TestAgentCommissionRecorderSkipsInvalidOrNonBillableUsage(t *testing.T) {
	t.Parallel()
	svc := &AffiliateService{}
	for _, usage := range []*UsageLog{
		nil,
		{UserID: 1, ActualCost: 0},
		{UserID: 1, ActualCost: -1},
	} {
		if err := svc.RecordAgentCommissionForUsage(context.Background(), usage); err != nil {
			t.Fatalf("invalid usage returned error: %v", err)
		}
	}
}
