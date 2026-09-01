package service

import (
	"context"
	"math"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// Agent commission rates are stored as basis points.  Keep the allowed rates
// deliberately small and explicit: these are the rates supported by the
// migrated New deployment and avoid accidentally granting an arbitrary rate.
const (
	AgentDefaultRateBPS   = 700
	AgentTierTwoRateBPS   = 1000
	AgentTierThreeRateBPS = 1300
	AgentRateBaseBPS      = 10000

	AgentTierTwoThreshold       = 100.0
	AgentTierThreeThreshold     = 10000.0
	AgentMinimumWithdrawal      = 10.0
	AgentPaymentQRCodeMaxLength = 60000
)

const (
	AgentWithdrawalPending  = "pending"
	AgentWithdrawalPaid     = "paid"
	AgentWithdrawalRejected = "rejected"
)

var (
	ErrAgentProfileNotFound = infraerrors.NotFound("AGENT_PROFILE_NOT_FOUND", "agent profile not found")
	ErrAgentUserNotFound    = infraerrors.NotFound("AGENT_USER_NOT_FOUND", "user not found")
	ErrAgentNested          = infraerrors.BadRequest("AGENT_NESTED_NOT_ALLOWED", "agent relationships can only have one level")
	ErrAgentSelfAssignment  = infraerrors.BadRequest("AGENT_SELF_ASSIGNMENT", "an agent cannot be assigned to itself")
	ErrAgentDisabled        = infraerrors.BadRequest("AGENT_DISABLED", "agent is disabled")
	ErrAgentInsufficient    = infraerrors.BadRequest("AGENT_INSUFFICIENT_COMMISSION", "insufficient commission balance")
	ErrAgentInvalidRate     = infraerrors.BadRequest("AGENT_INVALID_RATE", "commission rate must be 7%, 10%, or 13%")
	ErrAgentInvalidAmount   = infraerrors.BadRequest("AGENT_INVALID_AMOUNT", "invalid withdrawal amount")
	ErrAgentNoPayment       = infraerrors.BadRequest("AGENT_PAYMENT_REQUIRED", "payment account or QR code is required")
	ErrAgentWithdrawalState = infraerrors.Conflict("AGENT_WITHDRAWAL_STATE", "withdrawal is already processed")
)

// AgentProfile is the persisted configuration and commission counters for an
// agent. Amounts are user-facing currency values (the same decimal units as
// usage_logs.actual_cost), rather than upstream token cost.
type AgentProfile struct {
	UserID             int64     `json:"user_id"`
	Enabled            bool      `json:"enabled"`
	ManualRateBPS      int       `json:"manual_rate_bps"`
	CurrentRateBPS     int       `json:"current_rate_bps"`
	TotalCustomerUsage float64   `json:"total_customer_usage"`
	PendingCommission  float64   `json:"pending_commission"`
	TransferredAmount  float64   `json:"transferred_amount"`
	WithdrawingAmount  float64   `json:"withdrawing_amount"`
	WithdrawnAmount    float64   `json:"withdrawn_amount"`
	TotalCommission    float64   `json:"total_commission"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type AgentSummary struct {
	Enabled                 bool    `json:"enabled"`
	ManualRateBPS           int     `json:"manual_rate_bps"`
	CurrentRateBPS          int     `json:"current_rate_bps"`
	CommissionRate          float64 `json:"commission_rate"`
	CustomerCount           int64   `json:"customer_count"`
	TotalCustomerUsage      float64 `json:"total_customer_usage"`
	PendingCommission       float64 `json:"pending_commission"`
	WithdrawableCommission  float64 `json:"withdrawable_commission"`
	WithdrawingAmount       float64 `json:"withdrawing_amount"`
	TransferredAmount       float64 `json:"transferred_amount"`
	WithdrawnAmount         float64 `json:"withdrawn_amount"`
	TotalCommission         float64 `json:"total_commission"`
	PendingWithdrawalCount  int64   `json:"pending_withdrawal_count"`
	MinimumWithdrawalAmount float64 `json:"minimum_withdrawal_amount"`
	NextTierThreshold       float64 `json:"next_tier_threshold"`
	NextTierRateBPS         int     `json:"next_tier_rate_bps"`
}

// AgentCustomer intentionally represents only direct customers.  No field in
// this type contains a nested customer list; the service/repository query is
// always constrained by users.agent_id = agentID.
type AgentCustomer struct {
	UserID       int64     `json:"user_id"`
	Email        string    `json:"email"`
	Username     string    `json:"username"`
	TotalUsage   float64   `json:"total_usage"`
	RequestCount int64     `json:"request_count"`
	CreatedAt    time.Time `json:"created_at"`
}

type AgentCommission struct {
	ID                int64     `json:"id"`
	AgentUserID       int64     `json:"agent_user_id"`
	CustomerUserID    int64     `json:"customer_user_id"`
	UsageLogID        *int64    `json:"usage_log_id,omitempty"`
	RequestID         string    `json:"request_id"`
	IdempotencyKey    string    `json:"idempotency_key"`
	ModelName         string    `json:"model_name"`
	GroupName         string    `json:"group_name"`
	UsageAmount       float64   `json:"usage_amount"`
	CommissionAmount  float64   `json:"commission_amount"`
	CommissionRateBPS int       `json:"commission_rate_bps"`
	CreatedAt         time.Time `json:"created_at"`
}

type AgentWithdrawal struct {
	ID             int64      `json:"id"`
	AgentUserID    int64      `json:"agent_user_id"`
	Amount         float64    `json:"amount"`
	PaymentAccount string     `json:"payment_account"`
	PaymentQRCode  string     `json:"payment_qr_code,omitempty"`
	Note           string     `json:"note,omitempty"`
	AdminNote      string     `json:"admin_note,omitempty"`
	Status         string     `json:"status"`
	ProcessedBy    *int64     `json:"processed_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ProcessedAt    *time.Time `json:"processed_at,omitempty"`
}

// AgentProfileView is the compact admin list representation.  It deliberately
// reports direct customer counts and usage only.
type AgentProfileView struct {
	UserID             int64     `json:"user_id"`
	Email              string    `json:"email"`
	Username           string    `json:"username"`
	Enabled            bool      `json:"enabled"`
	ManualRateBPS      int       `json:"manual_rate_bps"`
	CurrentRateBPS     int       `json:"current_rate_bps"`
	RateBPS            int       `json:"rate_bps"`
	CustomerCount      int64     `json:"customer_count"`
	TotalCustomerUsage float64   `json:"total_customer_usage"`
	PendingCommission  float64   `json:"pending_commission"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type AgentProfileFilter struct {
	Search   string
	Page     int
	PageSize int
	// Scope is "active" (default) or "all".
	Scope string
}

type AgentWithdrawalFilter struct {
	Status   string
	Page     int
	PageSize int
}

// AgentRepository is intentionally separate from AffiliateRepository. The
// existing affiliate repository models recharge rebates and its test doubles
// must remain source-compatible. Implementations settle each usage event only
// against its direct agent; a user may also be an agent for other customers.
type AgentRepository interface {
	GetProfile(ctx context.Context, userID int64) (*AgentProfile, error)
	UpsertProfile(ctx context.Context, userID int64, enabled bool, manualRateBPS int) (*AgentProfile, error)
	AssignCustomer(ctx context.Context, customerUserID int64, agentUserID *int64) error
	GetSummary(ctx context.Context, agentUserID int64) (*AgentSummary, error)
	ListCustomers(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]AgentCustomer, int64, error)
	ListCommissions(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]AgentCommission, int64, error)
	ListWithdrawals(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]AgentWithdrawal, int64, error)
	CreateWithdrawal(ctx context.Context, agentUserID int64, amount float64, paymentAccount, paymentQRCode, note string) (*AgentWithdrawal, error)
	TransferCommission(ctx context.Context, agentUserID int64) (float64, error)
	ListProfiles(ctx context.Context, filter AgentProfileFilter) ([]AgentProfileView, int64, error)
	ListAdminWithdrawals(ctx context.Context, filter AgentWithdrawalFilter) ([]AgentWithdrawal, int64, error)
	ProcessWithdrawal(ctx context.Context, withdrawalID, adminUserID int64, status, adminNote string) (*AgentWithdrawal, error)
	RecordCommissionForUsage(ctx context.Context, usage *UsageLog) error
}

// AgentCommissionRecorder is the small optional hook consumed by the usage
// log repository.  Keeping it separate from UsageLogRepository avoids forcing
// every existing test stub to implement agent functionality.
type AgentCommissionRecorder interface {
	RecordAgentCommissionForUsage(ctx context.Context, usage *UsageLog) error
}

func (s *AffiliateService) SetAgentRepository(repo AgentRepository) {
	if s != nil {
		s.agentRepo = repo
	}
}

// ProvideAffiliateService keeps the historical NewAffiliateService
// constructor intact for tests and external callers while allowing Wire to
// inject the optional agent repository in production.
func ProvideAffiliateService(
	affiliateRepo AffiliateRepository,
	agentRepo AgentRepository,
	settingService *SettingService,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCacheService *BillingCacheService,
) *AffiliateService {
	svc := NewAffiliateService(affiliateRepo, settingService, authCacheInvalidator, billingCacheService)
	svc.SetAgentRepository(agentRepo)
	return svc
}

func (s *AffiliateService) AgentRepository() AgentRepository {
	if s == nil {
		return nil
	}
	return s.agentRepo
}

func (s *AffiliateService) agentServiceAvailable() (AgentRepository, error) {
	if s == nil || s.agentRepo == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "agent service unavailable")
	}
	return s.agentRepo, nil
}

func (s *AffiliateService) GetAgentSummary(ctx context.Context, userID int64) (*AgentSummary, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER", "invalid user")
	}
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, err
	}
	return r.GetSummary(ctx, userID)
}

func (s *AffiliateService) ListAgentCustomers(ctx context.Context, userID int64, params pagination.PaginationParams) ([]AgentCustomer, int64, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, 0, err
	}
	return r.ListCustomers(ctx, userID, normalizeAgentPagination(params))
}

func (s *AffiliateService) ListAgentCommissions(ctx context.Context, userID int64, params pagination.PaginationParams) ([]AgentCommission, int64, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, 0, err
	}
	return r.ListCommissions(ctx, userID, normalizeAgentPagination(params))
}

func (s *AffiliateService) ListAgentWithdrawals(ctx context.Context, userID int64, params pagination.PaginationParams) ([]AgentWithdrawal, int64, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, 0, err
	}
	return r.ListWithdrawals(ctx, userID, normalizeAgentPagination(params))
}

func (s *AffiliateService) CreateAgentWithdrawal(ctx context.Context, userID int64, amount float64, paymentAccount, paymentQRCode, note string) (*AgentWithdrawal, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, err
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < AgentMinimumWithdrawal {
		return nil, ErrAgentInvalidAmount
	}
	return r.CreateWithdrawal(ctx, userID, amount, strings.TrimSpace(paymentAccount), strings.TrimSpace(paymentQRCode), strings.TrimSpace(note))
}

func (s *AffiliateService) TransferAgentCommission(ctx context.Context, userID int64) (float64, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return 0, err
	}
	return r.TransferCommission(ctx, userID)
}

func (s *AffiliateService) AdminListAgentProfiles(ctx context.Context, filter AgentProfileFilter) ([]AgentProfileView, int64, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, 0, err
	}
	filter = normalizeAgentProfileFilter(filter)
	return r.ListProfiles(ctx, filter)
}

func (s *AffiliateService) AdminUpsertAgentProfile(ctx context.Context, userID int64, enabled bool, manualRateBPS int) (*AgentProfile, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER", "invalid user")
	}
	if !isAllowedAgentRateBPS(manualRateBPS) {
		return nil, ErrAgentInvalidRate
	}
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, err
	}
	return r.UpsertProfile(ctx, userID, enabled, manualRateBPS)
}

func (s *AffiliateService) AdminAssignAgentCustomer(ctx context.Context, customerUserID int64, agentUserID *int64) error {
	if customerUserID <= 0 {
		return infraerrors.BadRequest("INVALID_USER", "invalid customer user")
	}
	if agentUserID != nil && *agentUserID == customerUserID {
		return ErrAgentSelfAssignment
	}
	r, err := s.agentServiceAvailable()
	if err != nil {
		return err
	}
	return r.AssignCustomer(ctx, customerUserID, agentUserID)
}

func (s *AffiliateService) AdminListAgentCustomers(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]AgentCustomer, int64, error) {
	return s.ListAgentCustomers(ctx, agentUserID, params)
}

func (s *AffiliateService) AdminListAgentWithdrawals(ctx context.Context, filter AgentWithdrawalFilter) ([]AgentWithdrawal, int64, error) {
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, 0, err
	}
	filter = normalizeAgentWithdrawalFilter(filter)
	return r.ListAdminWithdrawals(ctx, filter)
}

func (s *AffiliateService) AdminProcessAgentWithdrawal(ctx context.Context, withdrawalID, adminUserID int64, status, adminNote string) (*AgentWithdrawal, error) {
	if withdrawalID <= 0 || adminUserID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_ID", "invalid withdrawal or administrator")
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != AgentWithdrawalPaid && status != AgentWithdrawalRejected {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "status must be paid or rejected")
	}
	r, err := s.agentServiceAvailable()
	if err != nil {
		return nil, err
	}
	return r.ProcessWithdrawal(ctx, withdrawalID, adminUserID, status, strings.TrimSpace(adminNote))
}

// RecordAgentCommissionForUsage satisfies AgentCommissionRecorder and is safe
// to call from the usage-log persistence path.  Repository errors are returned
// to the caller so the caller can log/queue them, but must not roll back the
// already persisted usage log.
func (s *AffiliateService) RecordAgentCommissionForUsage(ctx context.Context, usage *UsageLog) error {
	if usage == nil || usage.UserID <= 0 || usage.ActualCost <= 0 || math.IsNaN(usage.ActualCost) || math.IsInf(usage.ActualCost, 0) {
		return nil
	}
	r, err := s.agentServiceAvailable()
	if err != nil {
		// Agent migration is optional during a rolling upgrade.  Treat an absent
		// repository as a no-op so billing remains available.
		return nil
	}
	return r.RecordCommissionForUsage(ctx, usage)
}

func normalizeAgentPagination(p pagination.PaginationParams) pagination.PaginationParams {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > 1000 {
		p.PageSize = 1000
	}
	return p
}

func normalizeAgentProfileFilter(f AgentProfileFilter) AgentProfileFilter {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 20
	}
	if f.PageSize > 1000 {
		f.PageSize = 1000
	}
	f.Search = strings.TrimSpace(f.Search)
	f.Scope = strings.ToLower(strings.TrimSpace(f.Scope))
	if f.Scope != "all" {
		f.Scope = "active"
	}
	return f
}

func normalizeAgentWithdrawalFilter(f AgentWithdrawalFilter) AgentWithdrawalFilter {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 20
	}
	if f.PageSize > 1000 {
		f.PageSize = 1000
	}
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))
	return f
}

func isAllowedAgentRateBPS(rate int) bool {
	return rate == 0 || rate == AgentDefaultRateBPS || rate == AgentTierTwoRateBPS || rate == AgentTierThreeRateBPS
}

func resolveAgentRateBPS(manualRateBPS int, totalCustomerUsage float64) int {
	if manualRateBPS > 0 {
		return manualRateBPS
	}
	if totalCustomerUsage >= AgentTierThreeThreshold {
		return AgentTierThreeRateBPS
	}
	if totalCustomerUsage >= AgentTierTwoThreshold {
		return AgentTierTwoRateBPS
	}
	return AgentDefaultRateBPS
}

// ResolveAgentRateBPS exposes the deterministic tier calculation to the SQL
// repository while keeping the rate policy in the service package.
func ResolveAgentRateBPS(manualRateBPS int, totalCustomerUsage float64) int {
	return resolveAgentRateBPS(manualRateBPS, totalCustomerUsage)
}

func nextAgentTier(totalCustomerUsage float64, manualRateBPS int) (float64, int) {
	if manualRateBPS > 0 {
		return 0, 0
	}
	if totalCustomerUsage < AgentTierTwoThreshold {
		return AgentTierTwoThreshold, AgentTierTwoRateBPS
	}
	if totalCustomerUsage < AgentTierThreeThreshold {
		return AgentTierThreeThreshold, AgentTierThreeRateBPS
	}
	return 0, 0
}

// NextAgentTier returns the next automatic threshold and rate, or zeroes when
// a manual rate is active or all automatic tiers have been reached.
func NextAgentTier(totalCustomerUsage float64, manualRateBPS int) (float64, int) {
	return nextAgentTier(totalCustomerUsage, manualRateBPS)
}

func agentCommissionAmount(usageAmount float64, rateBPS int) float64 {
	if usageAmount <= 0 || rateBPS <= 0 {
		return 0
	}
	// Currency values are stored with eight decimal places throughout Sub.
	return math.Round(usageAmount*float64(rateBPS)/float64(AgentRateBaseBPS)*1e8) / 1e8
}

// AgentCommissionAmount calculates a commission rounded to Sub's eight
// decimal currency places.
func AgentCommissionAmount(usageAmount float64, rateBPS int) float64 {
	return agentCommissionAmount(usageAmount, rateBPS)
}
