package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// agentRepository uses raw SQL because agent_id and the agent tables are
// intentionally additive and are not part of the generated Ent schema yet.
// Keeping this repository independent also lets old test doubles and rolling
// deployments continue to use AffiliateRepository unchanged.
type agentRepository struct {
	client *dbent.Client
	db     *sql.DB
}

func NewAgentRepository(client *dbent.Client, db *sql.DB) service.AgentRepository {
	return &agentRepository{client: client, db: db}
}

func (r *agentRepository) unavailable() error {
	if r == nil || r.db == nil {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "agent repository unavailable")
	}
	return nil
}

func (r *agentRepository) GetProfile(ctx context.Context, userID int64) (*service.AgentProfile, error) {
	if err := r.unavailable(); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, service.ErrAgentUserNotFound
	}
	row := r.db.QueryRowContext(ctx, `
SELECT user_id, enabled, manual_rate_bps, current_rate_bps,
       total_customer_usage::double precision,
       pending_commission::double precision,
       transferred_amount::double precision,
       withdrawing_amount::double precision,
       withdrawn_amount::double precision,
       total_commission::double precision,
       created_at, updated_at
FROM agent_profiles
WHERE user_id = $1`, userID)
	profile, err := scanAgentProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAgentProfileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent profile: %w", err)
	}
	return profile, nil
}

func (r *agentRepository) UpsertProfile(ctx context.Context, userID int64, enabled bool, manualRateBPS int) (*service.AgentProfile, error) {
	if err := r.unavailable(); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, service.ErrAgentUserNotFound
	}
	if manualRateBPS != 0 && !isRepositoryAgentRateAllowed(manualRateBPS) {
		return nil, service.ErrAgentInvalidRate
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin agent profile transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var agentID sql.NullInt64
	var deletedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
SELECT agent_id, deleted_at
FROM users
WHERE id = $1
FOR UPDATE`, userID).Scan(&agentID, &deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrAgentUserNotFound
		}
		return nil, fmt.Errorf("lock agent user: %w", err)
	}
	if deletedAt.Valid {
		return nil, service.ErrAgentUserNotFound
	}
	// A user that is already a customer's customer cannot become an agent;
	// otherwise commissions could be forwarded through a second level.  The
	// role boundary applies even while a profile is disabled: a disabled agent
	// remains an agent until its profile is removed.
	if agentID.Valid && agentID.Int64 != 0 {
		return nil, service.ErrAgentNested
	}

	var totalUsage float64
	var existingManual int
	var existingEnabled bool
	var existingCurrent int
	row := tx.QueryRowContext(ctx, `
SELECT total_customer_usage::double precision, manual_rate_bps,
       enabled, current_rate_bps
FROM agent_profiles
WHERE user_id = $1
FOR UPDATE`, userID)
	profileExists := true
	if err := row.Scan(&totalUsage, &existingManual, &existingEnabled, &existingCurrent); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read agent profile: %w", err)
		}
		profileExists = false
	}
	if !profileExists {
		totalUsage = 0
		existingManual = 0
		existingEnabled = true
		existingCurrent = service.AgentDefaultRateBPS
	}
	currentRate := service.ResolveAgentRateBPS(manualRateBPS, totalUsage)
	if manualRateBPS == 0 && !profileExists {
		currentRate = service.AgentDefaultRateBPS
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_profiles (
    user_id, enabled, manual_rate_bps, current_rate_bps,
    total_customer_usage, pending_commission, transferred_amount,
    withdrawing_amount, withdrawn_amount, total_commission,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, 0, 0, 0, 0, 0, 0, NOW(), NOW())
ON CONFLICT (user_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    manual_rate_bps = EXCLUDED.manual_rate_bps,
    current_rate_bps = EXCLUDED.current_rate_bps,
    updated_at = NOW()`, userID, enabled, manualRateBPS, currentRate); err != nil {
		return nil, fmt.Errorf("upsert agent profile: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent profile: %w", err)
	}
	return r.GetProfile(ctx, userID)
}

func (r *agentRepository) AssignCustomer(ctx context.Context, customerUserID int64, agentUserID *int64) error {
	if err := r.unavailable(); err != nil {
		return err
	}
	if customerUserID <= 0 {
		return service.ErrAgentUserNotFound
	}
	if agentUserID != nil && *agentUserID == customerUserID {
		return service.ErrAgentSelfAssignment
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent assignment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var customerAgentID sql.NullInt64
	var customerDeletedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
SELECT agent_id, deleted_at
FROM users
WHERE id = $1
FOR UPDATE`, customerUserID).Scan(&customerAgentID, &customerDeletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAgentUserNotFound
		}
		return fmt.Errorf("lock customer user: %w", err)
	}
	if customerDeletedAt.Valid {
		return service.ErrAgentUserNotFound
	}

	if agentUserID == nil || *agentUserID <= 0 {
		if _, err := tx.ExecContext(ctx, "UPDATE users SET agent_id = NULL, updated_at = NOW() WHERE id = $1", customerUserID); err != nil {
			return fmt.Errorf("clear customer agent: %w", err)
		}
		return tx.Commit()
	}
	agentID := *agentUserID
	var parentAgentID sql.NullInt64
	var agentDeletedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
SELECT agent_id, deleted_at
FROM users
WHERE id = $1
FOR UPDATE`, agentID).Scan(&parentAgentID, &agentDeletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAgentUserNotFound
		}
		return fmt.Errorf("lock agent user: %w", err)
	}
	if agentDeletedAt.Valid {
		return service.ErrAgentUserNotFound
	}
	if parentAgentID.Valid && parentAgentID.Int64 != 0 {
		return service.ErrAgentNested
	}

	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM agent_profiles WHERE user_id = $1 FOR UPDATE", agentID).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAgentProfileNotFound
		}
		return fmt.Errorf("read agent profile for assignment: %w", err)
	}
	if !enabled {
		return service.ErrAgentDisabled
	}
	// Do not make an existing agent a customer.  This catches stale profiles
	// even if the agent_id column itself is currently NULL.
	var customerIsAgent bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM agent_profiles WHERE user_id = $1)", customerUserID).Scan(&customerIsAgent); err != nil {
		return fmt.Errorf("check nested customer: %w", err)
	}
	if customerIsAgent {
		return service.ErrAgentNested
	}

	if _, err := tx.ExecContext(ctx, "UPDATE users SET agent_id = $1, updated_at = NOW() WHERE id = $2", agentID, customerUserID); err != nil {
		return fmt.Errorf("assign customer agent: %w", err)
	}
	return tx.Commit()
}

func (r *agentRepository) GetSummary(ctx context.Context, agentUserID int64) (*service.AgentSummary, error) {
	if err := r.unavailable(); err != nil {
		return nil, err
	}
	if agentUserID <= 0 {
		return nil, service.ErrAgentUserNotFound
	}
	profile, err := r.GetProfile(ctx, agentUserID)
	if err != nil && !errors.Is(err, service.ErrAgentProfileNotFound) {
		return nil, err
	}
	var userExists bool
	if err := r.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)", agentUserID).Scan(&userExists); err != nil {
		return nil, fmt.Errorf("check agent user: %w", err)
	}
	if !userExists {
		return nil, service.ErrAgentUserNotFound
	}

	summary := &service.AgentSummary{
		Enabled:                 false,
		CurrentRateBPS:          service.AgentDefaultRateBPS,
		CommissionRate:          float64(service.AgentDefaultRateBPS) / float64(service.AgentRateBaseBPS),
		MinimumWithdrawalAmount: service.AgentMinimumWithdrawal,
	}
	if profile != nil {
		summary.Enabled = profile.Enabled
		summary.ManualRateBPS = profile.ManualRateBPS
		summary.CurrentRateBPS = service.ResolveAgentRateBPS(profile.ManualRateBPS, profile.TotalCustomerUsage)
		summary.CommissionRate = float64(summary.CurrentRateBPS) / float64(service.AgentRateBaseBPS)
		summary.TotalCustomerUsage = profile.TotalCustomerUsage
		summary.PendingCommission = profile.PendingCommission
		summary.WithdrawableCommission = profile.PendingCommission
		summary.WithdrawingAmount = profile.WithdrawingAmount
		summary.TransferredAmount = profile.TransferredAmount
		summary.WithdrawnAmount = profile.WithdrawnAmount
		summary.TotalCommission = profile.TotalCommission
	}
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE agent_id = $1 AND deleted_at IS NULL", agentUserID).Scan(&summary.CustomerCount); err != nil {
		return nil, fmt.Errorf("count agent customers: %w", err)
	}
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_withdrawals WHERE agent_user_id = $1 AND status = 'pending'", agentUserID).Scan(&summary.PendingWithdrawalCount); err != nil {
		return nil, fmt.Errorf("count pending withdrawals: %w", err)
	}
	summary.NextTierThreshold, summary.NextTierRateBPS = service.NextAgentTier(summary.TotalCustomerUsage, summary.ManualRateBPS)
	return summary, nil
}

func (r *agentRepository) ListCustomers(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]service.AgentCustomer, int64, error) {
	if err := r.unavailable(); err != nil {
		return nil, 0, err
	}
	params = normalizeRepositoryPagination(params)
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE agent_id = $1 AND deleted_at IS NULL", agentUserID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count agent customers: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT u.id,
       COALESCE(u.email, ''),
       COALESCE(u.username, ''),
       COALESCE(SUM(CASE WHEN ul.actual_cost > 0 THEN ul.actual_cost ELSE 0 END), 0)::double precision,
       COUNT(ul.id),
       u.created_at
FROM users u
LEFT JOIN usage_logs ul ON ul.user_id = u.id
WHERE u.agent_id = $1 AND u.deleted_at IS NULL
GROUP BY u.id, u.email, u.username, u.created_at
ORDER BY u.id DESC
LIMIT $2 OFFSET $3`, agentUserID, params.Limit(), params.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("list agent customers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AgentCustomer, 0)
	for rows.Next() {
		var item service.AgentCustomer
		if err := rows.Scan(&item.UserID, &item.Email, &item.Username, &item.TotalUsage, &item.RequestCount, &item.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan agent customer: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *agentRepository) ListCommissions(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]service.AgentCommission, int64, error) {
	if err := r.unavailable(); err != nil {
		return nil, 0, err
	}
	params = normalizeRepositoryPagination(params)
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_commissions WHERE agent_user_id = $1", agentUserID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count agent commissions: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id, agent_user_id, customer_user_id, usage_log_id,
       request_id, idempotency_key, model_name, group_name,
       usage_amount::double precision, commission_amount::double precision,
       commission_rate_bps, created_at
FROM agent_commissions
WHERE agent_user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`, agentUserID, params.Limit(), params.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("list agent commissions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AgentCommission, 0)
	for rows.Next() {
		item, err := scanAgentCommission(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *agentRepository) ListWithdrawals(ctx context.Context, agentUserID int64, params pagination.PaginationParams) ([]service.AgentWithdrawal, int64, error) {
	if err := r.unavailable(); err != nil {
		return nil, 0, err
	}
	params = normalizeRepositoryPagination(params)
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_withdrawals WHERE agent_user_id = $1", agentUserID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count agent withdrawals: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id, agent_user_id, amount::double precision, payment_account,
       payment_qr_code, note, admin_note, status, processed_by,
       created_at, updated_at, processed_at
FROM agent_withdrawals
WHERE agent_user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`, agentUserID, params.Limit(), params.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("list agent withdrawals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AgentWithdrawal, 0)
	for rows.Next() {
		item, err := scanAgentWithdrawal(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *agentRepository) CreateWithdrawal(ctx context.Context, agentUserID int64, amount float64, paymentAccount, paymentQRCode, note string) (*service.AgentWithdrawal, error) {
	if err := r.unavailable(); err != nil {
		return nil, err
	}
	if agentUserID <= 0 {
		return nil, service.ErrAgentUserNotFound
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < service.AgentMinimumWithdrawal {
		return nil, service.ErrAgentInvalidAmount
	}
	amount = roundAgentAmount(amount)
	if strings.TrimSpace(paymentAccount) == "" && strings.TrimSpace(paymentQRCode) == "" {
		return nil, service.ErrAgentNoPayment
	}
	if len(paymentQRCode) > service.AgentPaymentQRCodeMaxLength {
		return nil, infraerrors.BadRequest("AGENT_PAYMENT_QR_TOO_LARGE", "payment QR code is too large")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin withdrawal transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var enabled bool
	var pending float64
	if err := tx.QueryRowContext(ctx, `
SELECT enabled, pending_commission::double precision
FROM agent_profiles
WHERE user_id = $1
FOR UPDATE`, agentUserID).Scan(&enabled, &pending); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrAgentProfileNotFound
		}
		return nil, fmt.Errorf("lock agent profile: %w", err)
	}
	if !enabled {
		return nil, service.ErrAgentDisabled
	}
	if pending+1e-8 < amount {
		return nil, service.ErrAgentInsufficient
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
INSERT INTO agent_withdrawals (agent_user_id, amount, payment_account, payment_qr_code, note, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 'pending', NOW(), NOW())
RETURNING id`, agentUserID, amount, strings.TrimSpace(paymentAccount), strings.TrimSpace(paymentQRCode), strings.TrimSpace(note)).Scan(&id); err != nil {
		return nil, fmt.Errorf("create agent withdrawal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_profiles
SET pending_commission = pending_commission - $1,
    withdrawing_amount = withdrawing_amount + $1,
    updated_at = NOW()
WHERE user_id = $2`, amount, agentUserID); err != nil {
		return nil, fmt.Errorf("reserve agent withdrawal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent withdrawal: %w", err)
	}
	return r.getWithdrawalByID(ctx, id)
}

func (r *agentRepository) TransferCommission(ctx context.Context, agentUserID int64) (float64, error) {
	if err := r.unavailable(); err != nil {
		return 0, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin commission transfer: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var enabled bool
	var amount float64
	if err := tx.QueryRowContext(ctx, `
SELECT enabled, pending_commission::double precision
FROM agent_profiles
WHERE user_id = $1
FOR UPDATE`, agentUserID).Scan(&enabled, &amount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, service.ErrAgentProfileNotFound
		}
		return 0, fmt.Errorf("lock commission profile: %w", err)
	}
	if !enabled {
		return 0, service.ErrAgentDisabled
	}
	if amount <= 0 {
		return 0, service.ErrAgentInsufficient
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_profiles
SET pending_commission = 0,
    transferred_amount = transferred_amount + $1,
    updated_at = NOW()
WHERE user_id = $2`, amount, agentUserID); err != nil {
		return 0, fmt.Errorf("clear pending commission: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2", amount, agentUserID); err != nil {
		return 0, fmt.Errorf("credit agent balance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit commission transfer: %w", err)
	}
	return amount, nil
}

func (r *agentRepository) ListProfiles(ctx context.Context, filter service.AgentProfileFilter) ([]service.AgentProfileView, int64, error) {
	if err := r.unavailable(); err != nil {
		return nil, 0, err
	}
	filter = normalizeRepositoryAgentProfileFilter(filter)
	where, args := buildAgentProfileFilter(filter)
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_profiles ap JOIN users u ON u.id = ap.user_id `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count agent profiles: %w", err)
	}
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `
SELECT ap.user_id,
       COALESCE(u.email, ''), COALESCE(u.username, ''), ap.enabled,
       ap.manual_rate_bps, ap.current_rate_bps,
       COUNT(c.id),
       COALESCE(SUM(CASE WHEN ul.actual_cost > 0 THEN ul.actual_cost ELSE 0 END), 0)::double precision,
       ap.pending_commission::double precision,
       ap.created_at, ap.updated_at
FROM agent_profiles ap
JOIN users u ON u.id = ap.user_id
LEFT JOIN users c ON c.agent_id = ap.user_id AND c.deleted_at IS NULL
LEFT JOIN usage_logs ul ON ul.user_id = c.id
`+where+`
GROUP BY ap.user_id, u.email, u.username, ap.enabled, ap.manual_rate_bps, ap.current_rate_bps,
         ap.pending_commission, ap.created_at, ap.updated_at
ORDER BY ap.created_at DESC, ap.user_id DESC
LIMIT $`+fmt.Sprint(limitPos)+` OFFSET $`+fmt.Sprint(offsetPos), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list agent profiles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AgentProfileView, 0)
	for rows.Next() {
		var item service.AgentProfileView
		if err := rows.Scan(&item.UserID, &item.Email, &item.Username, &item.Enabled, &item.ManualRateBPS, &item.CurrentRateBPS, &item.CustomerCount, &item.TotalCustomerUsage, &item.PendingCommission, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan agent profile: %w", err)
		}
		item.RateBPS = item.CurrentRateBPS
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *agentRepository) ListAdminWithdrawals(ctx context.Context, filter service.AgentWithdrawalFilter) ([]service.AgentWithdrawal, int64, error) {
	if err := r.unavailable(); err != nil {
		return nil, 0, err
	}
	filter = normalizeRepositoryAgentWithdrawalFilter(filter)
	where := ""
	args := make([]any, 0, 1)
	if filter.Status != "" {
		where = "WHERE aw.status = $1"
		args = append(args, filter.Status)
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_withdrawals aw "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count agent withdrawals: %w", err)
	}
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `
SELECT aw.id, aw.agent_user_id, aw.amount::double precision,
       aw.payment_account, aw.payment_qr_code, aw.note, aw.admin_note,
       aw.status, aw.processed_by, aw.created_at, aw.updated_at, aw.processed_at
FROM agent_withdrawals aw
`+where+`
ORDER BY aw.created_at DESC, aw.id DESC
LIMIT $`+fmt.Sprint(limitPos)+` OFFSET $`+fmt.Sprint(offsetPos), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin withdrawals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AgentWithdrawal, 0)
	for rows.Next() {
		item, err := scanAgentWithdrawal(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *agentRepository) ProcessWithdrawal(ctx context.Context, withdrawalID, adminUserID int64, status, adminNote string) (*service.AgentWithdrawal, error) {
	if err := r.unavailable(); err != nil {
		return nil, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != service.AgentWithdrawalPaid && status != service.AgentWithdrawalRejected {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "status must be paid or rejected")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin withdrawal processing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var agentID int64
	var amount float64
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `
SELECT agent_user_id, amount::double precision, status
FROM agent_withdrawals
WHERE id = $1
FOR UPDATE`, withdrawalID).Scan(&agentID, &amount, &currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrAgentProfileNotFound
		}
		return nil, fmt.Errorf("lock withdrawal: %w", err)
	}
	if currentStatus != service.AgentWithdrawalPending {
		return nil, service.ErrAgentWithdrawalState
	}
	var profileEnabled bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM agent_profiles WHERE user_id = $1 FOR UPDATE", agentID).Scan(&profileEnabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrAgentProfileNotFound
		}
		return nil, fmt.Errorf("lock withdrawal profile: %w", err)
	}
	// A disabled agent's already-reserved withdrawal can still be processed;
	// disabling an account must not strand reserved funds.
	if status == service.AgentWithdrawalPaid {
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_profiles
SET withdrawing_amount = GREATEST(withdrawing_amount - $1, 0),
    withdrawn_amount = withdrawn_amount + $1,
    updated_at = NOW()
WHERE user_id = $2`, amount, agentID); err != nil {
			return nil, fmt.Errorf("settle paid withdrawal: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_profiles
SET withdrawing_amount = GREATEST(withdrawing_amount - $1, 0),
    pending_commission = pending_commission + $1,
    updated_at = NOW()
WHERE user_id = $2`, amount, agentID); err != nil {
			return nil, fmt.Errorf("restore rejected withdrawal: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_withdrawals
SET status = $1, admin_note = $2, processed_by = $3,
    processed_at = NOW(), updated_at = NOW()
WHERE id = $4`, status, strings.TrimSpace(adminNote), adminUserID, withdrawalID); err != nil {
		return nil, fmt.Errorf("update withdrawal status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit withdrawal processing: %w", err)
	}
	return r.getWithdrawalByID(ctx, withdrawalID)
}

// RecordCommissionForUsage records exactly one commission for a successful
// usage log. It locks the customer and profile in one transaction, inserts by
// a unique idempotency key, and updates counters only when the insert wins.
func (r *agentRepository) RecordCommissionForUsage(ctx context.Context, usage *service.UsageLog) error {
	if err := r.unavailable(); err != nil {
		return err
	}
	if usage == nil || usage.UserID <= 0 || usage.ActualCost <= 0 || math.IsNaN(usage.ActualCost) || math.IsInf(usage.ActualCost, 0) {
		return nil
	}
	amount := roundAgentAmount(usage.ActualCost)
	key := agentUsageIdempotencyKey(usage)
	if key == "" {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin usage commission transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var agentID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
SELECT agent_id
FROM users
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE`, usage.UserID).Scan(&agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load usage customer: %w", err)
	}
	if !agentID.Valid || agentID.Int64 <= 0 || agentID.Int64 == usage.UserID {
		return nil
	}

	// A dirty or concurrently migrated database may still contain a profile
	// for the usage customer.  Such a user is an agent, not a billable
	// downstream customer; skip rather than forwarding a second-level reward.
	var usageCustomerIsAgent bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM agent_profiles WHERE user_id = $1)", usage.UserID).Scan(&usageCustomerIsAgent); err != nil {
		return fmt.Errorf("check usage customer agent role: %w", err)
	}
	if usageCustomerIsAgent {
		return nil
	}

	// If the agent has subsequently become a customer, do not recurse or pay
	// an indirect parent.  This also protects databases migrated from old data.
	var parentAgent sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT agent_id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE", agentID.Int64).Scan(&parentAgent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load usage agent: %w", err)
	}
	if parentAgent.Valid && parentAgent.Int64 != 0 {
		return nil
	}

	var enabled bool
	var manualRate int
	var currentTotal float64
	if err := tx.QueryRowContext(ctx, `
SELECT enabled, manual_rate_bps, total_customer_usage::double precision
FROM agent_profiles
WHERE user_id = $1
FOR UPDATE`, agentID.Int64).Scan(&enabled, &manualRate, &currentTotal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load usage agent profile: %w", err)
	}
	if !enabled {
		return nil
	}

	// Check before insert for a cheap common retry path. The unique constraint
	// below remains authoritative under concurrent requests.
	var existing int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_commissions WHERE idempotency_key = $1", key).Scan(&existing); err != nil {
		return fmt.Errorf("check commission idempotency: %w", err)
	}
	if existing > 0 {
		return nil
	}

	rateBPS := service.ResolveAgentRateBPS(manualRate, currentTotal+amount)
	commission := service.AgentCommissionAmount(amount, rateBPS)
	modelName := strings.TrimSpace(usage.Model)
	groupName := ""
	if usage.Group != nil {
		groupName = strings.TrimSpace(usage.Group.Name)
	}
	var usageLogID any
	if usage.ID > 0 {
		usageLogID = usage.ID
	} else {
		usageLogID = nil
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO agent_commissions (
    agent_user_id, customer_user_id, usage_log_id, request_id,
    idempotency_key, model_name, group_name, usage_amount,
    commission_amount, commission_rate_bps, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
ON CONFLICT (idempotency_key) DO NOTHING`,
		agentID.Int64, usage.UserID, usageLogID, strings.TrimSpace(usage.RequestID), key,
		modelName, groupName, amount, commission, rateBPS)
	if err != nil {
		return fmt.Errorf("insert usage commission: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_profiles
SET total_customer_usage = total_customer_usage + $1,
    pending_commission = pending_commission + $2,
    total_commission = total_commission + $2,
    current_rate_bps = $3,
    updated_at = NOW()
WHERE user_id = $4`, amount, commission, rateBPS, agentID.Int64); err != nil {
		return fmt.Errorf("update agent commission counters: %w", err)
	}
	return tx.Commit()
}

func (r *agentRepository) getWithdrawalByID(ctx context.Context, id int64) (*service.AgentWithdrawal, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, agent_user_id, amount::double precision, payment_account,
       payment_qr_code, note, admin_note, status, processed_by,
       created_at, updated_at, processed_at
FROM agent_withdrawals WHERE id = $1`, id)
	item, err := scanAgentWithdrawal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAgentProfileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent withdrawal: %w", err)
	}
	return item, nil
}

type agentScanner interface {
	Scan(dest ...any) error
}

func scanAgentProfile(row agentScanner) (*service.AgentProfile, error) {
	var p service.AgentProfile
	return &p, row.Scan(
		&p.UserID, &p.Enabled, &p.ManualRateBPS, &p.CurrentRateBPS,
		&p.TotalCustomerUsage, &p.PendingCommission, &p.TransferredAmount,
		&p.WithdrawingAmount, &p.WithdrawnAmount, &p.TotalCommission,
		&p.CreatedAt, &p.UpdatedAt,
	)
}

func scanAgentCommission(row agentScanner) (*service.AgentCommission, error) {
	var c service.AgentCommission
	var usageLogID sql.NullInt64
	err := row.Scan(
		&c.ID, &c.AgentUserID, &c.CustomerUserID, &usageLogID,
		&c.RequestID, &c.IdempotencyKey, &c.ModelName, &c.GroupName,
		&c.UsageAmount, &c.CommissionAmount, &c.CommissionRateBPS, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan agent commission: %w", err)
	}
	if usageLogID.Valid {
		c.UsageLogID = &usageLogID.Int64
	}
	return &c, nil
}

func scanAgentWithdrawal(row agentScanner) (*service.AgentWithdrawal, error) {
	var w service.AgentWithdrawal
	var processedBy sql.NullInt64
	var processedAt sql.NullTime
	err := row.Scan(
		&w.ID, &w.AgentUserID, &w.Amount, &w.PaymentAccount, &w.PaymentQRCode,
		&w.Note, &w.AdminNote, &w.Status, &processedBy, &w.CreatedAt,
		&w.UpdatedAt, &processedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan agent withdrawal: %w", err)
	}
	if processedBy.Valid {
		w.ProcessedBy = &processedBy.Int64
	}
	if processedAt.Valid {
		w.ProcessedAt = &processedAt.Time
	}
	return &w, nil
}

func normalizeRepositoryPagination(p pagination.PaginationParams) pagination.PaginationParams {
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

func normalizeRepositoryAgentProfileFilter(f service.AgentProfileFilter) service.AgentProfileFilter {
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

func normalizeRepositoryAgentWithdrawalFilter(f service.AgentWithdrawalFilter) service.AgentWithdrawalFilter {
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
	if f.Status != "" && f.Status != service.AgentWithdrawalPending && f.Status != service.AgentWithdrawalPaid && f.Status != service.AgentWithdrawalRejected {
		f.Status = ""
	}
	return f
}

func buildAgentProfileFilter(f service.AgentProfileFilter) (string, []any) {
	clauses := []string{"u.deleted_at IS NULL"}
	args := make([]any, 0, 2)
	if f.Scope != "all" {
		clauses = append(clauses, "ap.enabled = TRUE")
	}
	if f.Search != "" {
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		clauses = append(clauses, "(LOWER(COALESCE(u.email, '')) LIKE "+placeholder+" OR LOWER(COALESCE(u.username, '')) LIKE "+placeholder+" OR u.id::text LIKE "+placeholder+")")
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func isRepositoryAgentRateAllowed(rate int) bool {
	return rate == 0 || rate == service.AgentDefaultRateBPS || rate == service.AgentTierTwoRateBPS || rate == service.AgentTierThreeRateBPS
}

func roundAgentAmount(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func agentUsageIdempotencyKey(usage *service.UsageLog) string {
	if usage == nil {
		return ""
	}
	if usage.ID > 0 {
		return fmt.Sprintf("usage-log:%d", usage.ID)
	}
	if requestID := strings.TrimSpace(usage.RequestID); requestID != "" {
		return fmt.Sprintf("request:%d:%d:%s", usage.UserID, usage.APIKeyID, requestID)
	}
	// Some legacy paths create a log without either identifier.  A stable hash
	// keeps retries idempotent without relying on process-local state.
	fingerprint := fmt.Sprintf("%d:%d:%d:%s:%s:%.8f:%d", usage.UserID, usage.APIKeyID, usage.AccountID, usage.Model, usage.RequestedModel, usage.ActualCost, usage.CreatedAt.UnixNano())
	sum := sha256.Sum256([]byte(fingerprint))
	return "fingerprint:" + hex.EncodeToString(sum[:])
}
