/**
 * One-level agent (代理) APIs.
 *
 * Agent relationships are intentionally separate from the invitation-rebate
 * APIs. A customer has at most one agent and an agent cannot itself be a
 * customer of another agent.
 */

import { apiClient } from './client'
import type { PaginatedResponse } from '@/types'

export interface AgentSummary {
  enabled: boolean
  manual_rate_bps: number
  current_rate_bps: number
  commission_rate: number
  customer_count: number
  total_customer_usage: number
  pending_commission: number
  withdrawable_commission: number
  withdrawing_amount: number
  transferred_amount: number
  withdrawn_amount: number
  total_commission: number
  pending_withdrawal_count: number
  minimum_withdrawal_amount: number
  next_tier_threshold: number
  next_tier_rate_bps: number
}

/** A customer row is always a direct child of the requested agent. */
export interface AgentCustomer {
  user_id: number
  username: string
  email: string
  total_usage: number
  request_count: number
  created_at: string | number
}

export interface AgentCommission {
  id: number
  agent_user_id: number
  customer_user_id: number
  usage_log_id?: number
  request_id: string
  idempotency_key: string
  model_name: string
  group_name: string
  usage_amount: number
  commission_amount: number
  commission_rate_bps: number
  created_at: string | number
}

export type AgentWithdrawalStatus = 'pending' | 'paid' | 'rejected'

export interface AgentWithdrawal {
  id: number
  agent_user_id: number
  amount: number
  payment_account: string
  payment_qr_code?: string
  note?: string
  admin_note?: string
  status: AgentWithdrawalStatus
  processed_by?: number
  created_at: string | number
  updated_at: string | number
  processed_at?: string | number | null
}

export interface AgentProfile {
  user_id: number
  username: string
  email: string
  enabled: boolean
  rate_bps: number
  manual_rate_bps: number
  current_rate_bps: number
  customer_count: number
  total_customer_usage: number
  pending_commission: number
  created_at: string | number
  updated_at: string | number
}

export interface AssignAgentRequest {
  user_id: number
  /** Use 0 to clear the direct assignment. */
  agent_id: number
}

export interface UpdateAgentProfileRequest {
  user_id: number
  enabled?: boolean
  /** 0 selects the automatic tier; otherwise use 700/1000/1300 bps. */
  manual_rate_bps?: number
}

export interface CreateAgentWithdrawalRequest {
  amount: number
  payment_account: string
  payment_qr_code?: string
  note?: string
}

export interface ProcessAgentWithdrawalRequest {
  status: Exclude<AgentWithdrawalStatus, 'pending'>
  admin_note?: string
}

const pageParams = (page = 1, pageSize = 20) => ({
  page,
  page_size: pageSize,
})

export async function getAgentSummary(): Promise<AgentSummary> {
  const { data } = await apiClient.get<AgentSummary>('/user/agent/summary')
  return data
}

export async function listAgentCustomers(
  page = 1,
  pageSize = 20,
): Promise<PaginatedResponse<AgentCustomer>> {
  const { data } = await apiClient.get<PaginatedResponse<AgentCustomer>>(
    '/user/agent/customers',
    { params: pageParams(page, pageSize) },
  )
  return data
}

export async function listAgentCommissions(
  page = 1,
  pageSize = 20,
): Promise<PaginatedResponse<AgentCommission>> {
  const { data } = await apiClient.get<PaginatedResponse<AgentCommission>>(
    '/user/agent/commissions',
    { params: pageParams(page, pageSize) },
  )
  return data
}

export async function listAgentWithdrawals(
  page = 1,
  pageSize = 20,
): Promise<PaginatedResponse<AgentWithdrawal>> {
  const { data } = await apiClient.get<PaginatedResponse<AgentWithdrawal>>(
    '/user/agent/withdrawals',
    { params: pageParams(page, pageSize) },
  )
  return data
}

export async function transferAgentCommission(): Promise<{ transferred_amount: number }> {
  const { data } = await apiClient.post<{ transferred_amount: number }>('/user/agent/transfer')
  return data
}

export async function createAgentWithdrawal(
  payload: CreateAgentWithdrawalRequest,
): Promise<AgentWithdrawal> {
  const { data } = await apiClient.post<AgentWithdrawal>('/user/agent/withdrawals', payload)
  return data
}

export async function listAdminAgentProfiles(
  page = 1,
  pageSize = 20,
  search = '',
): Promise<PaginatedResponse<AgentProfile>> {
  const { data } = await apiClient.get<PaginatedResponse<AgentProfile>>(
    '/admin/agents/profiles',
    { params: { ...pageParams(page, pageSize), search } },
  )
  return data
}

export async function updateAdminAgentProfile(
  payload: UpdateAgentProfileRequest,
): Promise<AgentProfile> {
  const { data } = await apiClient.put<AgentProfile>(
    `/admin/agents/profiles/${payload.user_id}`,
    payload,
  )
  return data
}

export async function assignAdminAgentCustomer(
  payload: AssignAgentRequest,
): Promise<void> {
  await apiClient.post('/admin/agents/assign', payload)
}

export async function listAdminAgentCustomers(
  agentId: number,
  page = 1,
  pageSize = 50,
): Promise<PaginatedResponse<AgentCustomer>> {
  const { data } = await apiClient.get<PaginatedResponse<AgentCustomer>>(
    `/admin/agents/${agentId}/customers`,
    { params: pageParams(page, pageSize) },
  )
  return data
}

export async function listAdminAgentWithdrawals(
  page = 1,
  pageSize = 20,
  status: AgentWithdrawalStatus | '' = '',
): Promise<PaginatedResponse<AgentWithdrawal>> {
  const { data } = await apiClient.get<PaginatedResponse<AgentWithdrawal>>(
    '/admin/agents/withdrawals',
    { params: { ...pageParams(page, pageSize), status: status || undefined } },
  )
  return data
}

export async function processAdminAgentWithdrawal(
  id: number,
  payload: ProcessAgentWithdrawalRequest,
): Promise<AgentWithdrawal> {
  const { data } = await apiClient.post<AgentWithdrawal>(
    `/admin/agents/withdrawals/${id}/process`,
    payload,
  )
  return data
}

const agentAPI = {
  getAgentSummary,
  listAgentCustomers,
  listAgentCommissions,
  listAgentWithdrawals,
  transferAgentCommission,
  createAgentWithdrawal,
  listAdminAgentProfiles,
  updateAdminAgentProfile,
  assignAdminAgentCustomer,
  listAdminAgentCustomers,
  listAdminAgentWithdrawals,
  processAdminAgentWithdrawal,
}

export default agentAPI
