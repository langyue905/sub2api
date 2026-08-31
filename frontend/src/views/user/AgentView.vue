<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex justify-center py-12">
        <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
      </div>

      <template v-else-if="summary">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('agent.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('agent.description') }}</p>
          </div>
          <span :class="summary.enabled ? 'badge badge-success' : 'badge badge-danger'">
            {{ summary.enabled ? t('agent.enabled') : t('agent.disabled') }}
          </span>
        </div>

        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('agent.stats.rate') }}</p>
            <p class="mt-2 text-2xl font-semibold text-primary-600 dark:text-primary-400">{{ formatRate(summary.current_rate_bps) }}</p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('agent.stats.directCustomers') }}</p>
            <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ summary.customer_count }}</p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('agent.stats.pending') }}</p>
            <p class="mt-2 text-2xl font-semibold text-emerald-600 dark:text-emerald-400">{{ formatAmount(summary.withdrawable_commission) }}</p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('agent.stats.total') }}</p>
            <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatAmount(summary.total_commission) }}</p>
          </div>
        </div>

        <div class="card p-5">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agent.balance.title') }}</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('agent.balance.description') }}</p>
            </div>
            <button class="btn btn-primary" :disabled="transferring || Number(summary.withdrawable_commission) <= 0" @click="transfer">
              <Icon name="dollar" size="sm" />
              <span>{{ transferring ? t('common.saving') : t('agent.balance.transfer') }}</span>
            </button>
          </div>
        </div>

        <div class="card overflow-hidden">
          <div class="flex flex-wrap items-center gap-2 border-b border-gray-200 p-4 dark:border-dark-700">
            <button v-for="tab in tabs" :key="tab" class="btn btn-sm" :class="activeTab === tab ? 'btn-primary' : 'btn-secondary'" @click="activeTab = tab">
              {{ t(`agent.tabs.${tab}`) }}
            </button>
          </div>

          <div v-if="activeTab === 'customers'" class="overflow-x-auto">
            <p class="px-5 pt-4 text-xs text-gray-500 dark:text-dark-400">{{ t('agent.customers.hint') }}</p>
            <table class="min-w-[680px] w-full text-left">
              <thead><tr><th class="table-th">{{ t('agent.customers.customer') }}</th><th class="table-th">{{ t('agent.customers.usage') }}</th><th class="table-th">{{ t('agent.customers.requests') }}</th><th class="table-th">{{ t('agent.customers.joinedAt') }}</th></tr></thead>
              <tbody>
                <tr v-if="customers.length === 0"><td colspan="4" class="table-td text-center text-gray-500 dark:text-dark-400">{{ t('empty.noData') }}</td></tr>
                <tr v-for="customer in customers" :key="customer.user_id" class="border-b border-gray-100 dark:border-dark-700">
                  <td class="table-td"><div class="font-mono">#{{ customer.user_id }}</div><div>{{ customer.email || '-' }}</div><div class="text-xs text-gray-500 dark:text-dark-400">{{ customer.username || '-' }}</div></td>
                  <td class="table-td">{{ formatAmount(customer.total_usage) }}</td>
                  <td class="table-td">{{ customer.request_count }}</td>
                  <td class="table-td">{{ formatDate(customer.created_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-else-if="activeTab === 'commissions'" class="overflow-x-auto">
            <table class="min-w-[720px] w-full text-left">
              <thead><tr><th class="table-th">{{ t('agent.commissions.customer') }}</th><th class="table-th">{{ t('agent.commissions.model') }}</th><th class="table-th">{{ t('agent.commissions.usage') }}</th><th class="table-th">{{ t('agent.commissions.commission') }}</th><th class="table-th">{{ t('agent.commissions.createdAt') }}</th></tr></thead>
              <tbody>
                <tr v-if="commissions.length === 0"><td colspan="5" class="table-td text-center text-gray-500 dark:text-dark-400">{{ t('empty.noData') }}</td></tr>
                <tr v-for="item in commissions" :key="item.id" class="border-b border-gray-100 dark:border-dark-700">
                  <td class="table-td font-mono">#{{ item.customer_user_id }}</td>
                  <td class="table-td">{{ item.model_name || '-' }}</td>
                  <td class="table-td">{{ formatAmount(item.usage_amount) }}</td>
                  <td class="table-td font-medium text-emerald-600 dark:text-emerald-400">{{ formatAmount(item.commission_amount) }}</td>
                  <td class="table-td">{{ formatDate(item.created_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-else class="space-y-4 p-5">
            <form class="grid max-w-xl gap-3" @submit.prevent="submitWithdrawal">
              <label><span class="label-text">{{ t('agent.withdrawals.amount') }}</span><input v-model.number="withdrawal.amount" class="input" type="number" min="0.01" step="0.01" required /></label>
              <label><span class="label-text">{{ t('agent.withdrawals.paymentAccount') }}</span><input v-model="withdrawal.payment_account" class="input" required /></label>
              <label><span class="label-text">{{ t('agent.withdrawals.note') }}</span><textarea v-model="withdrawal.note" class="input min-h-20" maxlength="500"></textarea></label>
              <button class="btn btn-primary w-fit" :disabled="withdrawing" type="submit"><Icon name="arrowUp" size="sm" /><span>{{ withdrawing ? t('common.saving') : t('agent.withdrawals.submit') }}</span></button>
            </form>
            <div class="overflow-x-auto">
              <table class="min-w-[700px] w-full text-left">
                <thead><tr><th class="table-th">{{ t('agent.withdrawals.amount') }}</th><th class="table-th">{{ t('agent.withdrawals.paymentAccount') }}</th><th class="table-th">{{ t('agent.withdrawals.status') }}</th><th class="table-th">{{ t('agent.withdrawals.createdAt') }}</th></tr></thead>
                <tbody>
                  <tr v-if="withdrawals.length === 0"><td colspan="4" class="table-td text-center text-gray-500 dark:text-dark-400">{{ t('empty.noData') }}</td></tr>
                  <tr v-for="item in withdrawals" :key="item.id" class="border-b border-gray-100 dark:border-dark-700"><td class="table-td">{{ formatAmount(item.amount) }}</td><td class="table-td">{{ item.payment_account || '-' }}</td><td class="table-td"><span :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span></td><td class="table-td">{{ formatDate(item.created_at) }}</td></tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import agentAPI, { type AgentCommission, type AgentCustomer, type AgentSummary, type AgentWithdrawal } from '@/api/agent'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDisplayDateTime } from '@/utils/format'

type AgentTab = 'customers' | 'commissions' | 'withdrawals'
const tabs: AgentTab[] = ['customers', 'commissions', 'withdrawals']
const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const summary = ref<AgentSummary | null>(null)
const customers = ref<AgentCustomer[]>([])
const commissions = ref<AgentCommission[]>([])
const withdrawals = ref<AgentWithdrawal[]>([])
const activeTab = ref<AgentTab>('customers')
const transferring = ref(false)
const withdrawing = ref(false)
const withdrawal = reactive({ amount: 0, payment_account: '', note: '' })

function showError(error: unknown) {
  appStore.showError(extractI18nErrorMessage(error, t, 'agent.errors', t('common.error')))
}

async function load() {
  loading.value = true
  try {
    summary.value = await agentAPI.getAgentSummary()
    const [customerPage, commissionPage, withdrawalPage] = await Promise.all([
      agentAPI.listAgentCustomers(1, 100),
      agentAPI.listAgentCommissions(1, 100),
      agentAPI.listAgentWithdrawals(1, 100),
    ])
    customers.value = customerPage.items || []
    commissions.value = commissionPage.items || []
    withdrawals.value = withdrawalPage.items || []
  } catch (error) {
    showError(error)
  } finally {
    loading.value = false
  }
}

async function transfer() {
  if (transferring.value) return
  transferring.value = true
  try {
    await agentAPI.transferAgentCommission()
    appStore.showSuccess(t('agent.balance.success'))
    await load()
  } catch (error) {
    showError(error)
  } finally {
    transferring.value = false
  }
}

async function submitWithdrawal() {
  if (withdrawing.value || withdrawal.amount <= 0) return
  withdrawing.value = true
  try {
    await agentAPI.createAgentWithdrawal({ ...withdrawal })
    appStore.showSuccess(t('agent.withdrawals.success'))
    withdrawal.amount = 0
    withdrawal.payment_account = ''
    withdrawal.note = ''
    await load()
  } catch (error) {
    showError(error)
  } finally {
    withdrawing.value = false
  }
}

function formatAmount(value: number | null | undefined) { return `$${Number(value || 0).toFixed(2)}` }
function formatRate(value: number | null | undefined) { return `${(Number(value || 0) / 100).toFixed(2).replace(/\.00$/, '')}%` }
function formatDate(value: string | number | null | undefined) {
  if (value === null || value === undefined || value === '') return '-'
  if (typeof value === 'number') return formatDisplayDateTime(new Date(value > 10_000_000_000 ? value : value * 1000).toISOString())
  return formatDisplayDateTime(value)
}
function statusLabel(status: AgentWithdrawal['status']) { return status === 'pending' ? t('agent.status.pending') : status === 'paid' ? t('agent.status.paid') : status === 'rejected' ? t('agent.status.rejected') : t('agent.status.unknown') }
function statusClass(status: AgentWithdrawal['status']) { return status === 'pending' ? 'badge badge-warning' : status === 'paid' ? 'badge badge-success' : status === 'rejected' ? 'badge badge-danger' : 'badge' }

onMounted(() => { void load() })
</script>

<style scoped>
.table-th { @apply whitespace-nowrap border-b border-gray-200 bg-gray-50/80 px-5 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-600 dark:border-dark-700 dark:bg-dark-800/80 dark:text-dark-300; }
.table-td { @apply whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300; }
.label-text { @apply mb-1 block text-xs font-medium text-gray-600 dark:text-dark-300; }
</style>
