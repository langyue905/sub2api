package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestAgentRepositoryAssignCustomerAllowsAgentWithAnUpstreamAgent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer db.Close()

	repo := &agentRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT agent_id, deleted_at FROM users WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(70)).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id", "deleted_at"}).AddRow(nil, nil))
	mock.ExpectQuery(`SELECT agent_id, deleted_at FROM users WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(48)).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id", "deleted_at"}).AddRow(int64(40), nil))
	mock.ExpectQuery(`SELECT enabled FROM agent_profiles WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(48)).
		WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	mock.ExpectExec(`UPDATE users SET agent_id = \$1, updated_at = NOW\(\) WHERE id = \$2`).
		WithArgs(int64(48), int64(70)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.AssignCustomer(context.Background(), 70, int64Ptr(48)); err != nil {
		t.Fatalf("assigning a customer to an agent that is itself a customer: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestAgentRepositoryUpsertProfileAllowsExistingCustomer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer db.Close()

	repo := &agentRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT agent_id, deleted_at FROM users WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(48)).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id", "deleted_at"}).AddRow(int64(40), nil))
	mock.ExpectQuery(`SELECT total_customer_usage::double precision, manual_rate_bps, enabled, current_rate_bps FROM agent_profiles WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(48)).
		WillReturnRows(sqlmock.NewRows([]string{"total_customer_usage", "manual_rate_bps", "enabled", "current_rate_bps"}))
	mock.ExpectExec(`INSERT INTO agent_profiles`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT user_id, enabled, manual_rate_bps, current_rate_bps`).
		WithArgs(int64(48)).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "enabled", "manual_rate_bps", "current_rate_bps",
			"total_customer_usage", "pending_commission", "transferred_amount",
			"withdrawing_amount", "withdrawn_amount", "total_commission",
			"created_at", "updated_at",
		}).AddRow(
			int64(48), true, 0, service.AgentDefaultRateBPS,
			0.0, 0.0, 0.0, 0.0, 0.0, 0.0,
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		))

	if _, err := repo.UpsertProfile(context.Background(), 48, true, 0); err != nil {
		t.Fatalf("upserting profile for an existing customer: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestAgentRepositoryRecordsDirectCommissionForAgentCustomer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer db.Close()

	repo := &agentRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT agent_id FROM users WHERE id = \$1 AND deleted_at IS NULL FOR UPDATE`).
		WithArgs(int64(70)).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id"}).AddRow(int64(48)))
	mock.ExpectQuery(`SELECT enabled, manual_rate_bps, total_customer_usage::double precision FROM agent_profiles WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(48)).
		WillReturnRows(sqlmock.NewRows([]string{"enabled", "manual_rate_bps", "total_customer_usage"}).AddRow(true, 0, 0.0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM agent_commissions WHERE idempotency_key = \$1`).
		WithArgs("usage-log:123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectExec(`INSERT INTO agent_commissions`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE agent_profiles SET total_customer_usage = total_customer_usage \+ \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	usage := &service.UsageLog{ID: 123, UserID: 70, ActualCost: 10, Model: "model"}
	if err := repo.RecordCommissionForUsage(context.Background(), usage); err != nil {
		t.Fatalf("recording direct commission for an agent customer: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
