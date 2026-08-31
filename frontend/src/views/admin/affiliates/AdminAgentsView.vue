<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.agents.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('admin.agents.description') }}</p>
          </div>
          <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadActiveTab">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            <span>{{ t('common.refresh') }}</span>
          </button>
        </div>
      </template>

      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex items-center gap-2">
            <button
              class="btn btn-sm"
              :class="activeTab === 'profiles' ? 'btn-primary' : 'btn-secondary'"
              @click="switchTab('profiles')"
            >
              {{ t('admin.agents.tabs.profiles') }}
            </button>
            <button
              class="btn btn-sm"
              :class="activeTab === 'withdrawals' ? 'btn-primary' : 'btn-secondary'"
              @click="switchTab('withdrawals')"
            >
              {{ t('admin.agents.tabs.withdrawals') }}
            </button>
          </div>

          <template v-if="activeTab === 'profiles'">
            <div class="relative w-full md:w-80">
              <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                v-model="profileKeyword"
                class="input pl-10"
                :placeholder="t('admin.agents.searchPlaceholder')"
                @keydown.enter="reloadProfiles"
              />
            </div>
            <button class="btn btn-secondary btn-sm" :disabled="loading" @click="reloadProfiles">
              <Icon name="search" size="sm" />
              <span>{{ t('common.search') }}</span>
            </button>
          </template>

          <template v-else>
            <select v-model="withdrawalStatus" class="input w-full sm:w-44" @change="reloadWithdrawals">
              <option value="">{{ t('admin.agents.status.all') }}</option>
              <option value="pending">{{ t('admin.agents.status.pending') }}</option>
              <option value="paid">{{ t('admin.agents.status.paid') }}</option>
              <option value="rejected">{{ t('admin.agents.status.rejected') }}</option>
            </select>
          </template>
        </div>
      </template>

      <template #table>
        <div class="overflow-x-auto">
          <template v-if="activeTab === 'profiles'">
            <table class="min-w-[960px] w-full text-left">
              <thead>
                <tr>
                  <th class="table-th">{{ t('admin.agents.columns.agent') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.customers') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.usage') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.rate') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.pending') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.status') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!loading && profiles.length === 0">
                  <td colspan="7" class="table-td text-center text-gray-500 dark:text-dark-400">{{ t('empty.noData') }}</td>
                </tr>
                <tr v-for="profile in profiles" :key="profile.user_id" class="border-b border-gray-100 dark:border-dark-700">
                  <td class="table-td">
                    <div class="font-mono text-sm text-gray-900 dark:text-white">#{{ profile.user_id }}</div>
                    <div class="max-w-56 truncate text-sm text-gray-700 dark:text-gray-300">{{ profile.email || '-' }}</div>
                    <div class="max-w-56 truncate text-xs text-gray-500 dark:text-dark-400">{{ profile.username || '-' }}</div>
                  </td>
                  <td class="table-td">
                    <button class="btn btn-ghost btn-sm" @click="openCustomers(profile)">
                      <Icon name="users" size="sm" />
                      <span>{{ profile.customer_count }}</span>
                    </button>
                  </td>
                  <td class="table-td">{{ formatAmount(profile.total_customer_usage) }}</td>
                  <td class="table-td">
                    <select
                      class="input input-sm w-32"
                      :value="profile.manual_rate_bps > 0 ? String(profile.manual_rate_bps) : 'auto'"
                      :disabled="updatingUserId === profile.user_id"
                      @change="changeRate(profile, ($event.target as HTMLSelectElement).value)"
                    >
                      <option value="auto">{{ t('admin.agents.rate.auto') }} ({{ formatRate(profile.current_rate_bps) }})</option>
                      <option value="700">7%</option>
                      <option value="1000">10%</option>
                      <option value="1300">13%</option>
                    </select>
                  </td>
                  <td class="table-td font-medium text-emerald-600 dark:text-emerald-400">{{ formatAmount(profile.pending_commission) }}</td>
                  <td class="table-td">
                    <span :class="profile.enabled ? 'badge badge-success' : 'badge badge-danger'">
                      {{ profile.enabled ? t('admin.agents.status.enabled') : t('admin.agents.status.disabled') }}
                    </span>
                  </td>
                  <td class="table-td">
                    <button
                      class="btn btn-sm"
                      :class="profile.enabled ? 'btn-danger' : 'btn-secondary'"
                      :disabled="updatingUserId === profile.user_id"
                      @click="toggleProfile(profile)"
                    >
                      {{ profile.enabled ? t('admin.agents.actions.disable') : t('admin.agents.actions.enable') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>

            <div class="border-t border-gray-200 p-4 dark:border-dark-700">
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.agents.assign.title') }}</h3>
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.agents.assign.hint') }}</p>
              <div class="mt-3 flex flex-wrap items-end gap-3">
                <label class="min-w-40 flex-1">
                  <span class="label-text">{{ t('admin.agents.assign.customerId') }}</span>
                  <input v-model="assignCustomerId" class="input" inputmode="numeric" />
                </label>
                <label class="min-w-40 flex-1">
                  <span class="label-text">{{ t('admin.agents.assign.agentId') }}</span>
                  <input v-model="assignAgentId" class="input" inputmode="numeric" />
                </label>
                <button class="btn btn-primary" :disabled="assigning" @click="assignCustomer">
                  <Icon name="link" size="sm" />
                  <span>{{ assigning ? t('common.saving') : t('admin.agents.assign.submit') }}</span>
                </button>
              </div>
            </div>
          </template>

          <template v-else>
            <table class="min-w-[900px] w-full text-left">
              <thead>
                <tr>
                  <th class="table-th">{{ t('admin.agents.columns.agent') }}</th>
                  <th class="table-th">{{ t('admin.agents.withdrawals.amount') }}</th>
                  <th class="table-th">{{ t('admin.agents.withdrawals.payment') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.status') }}</th>
                  <th class="table-th">{{ t('admin.agents.withdrawals.note') }}</th>
                  <th class="table-th">{{ t('admin.agents.columns.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!loading && withdrawals.length === 0">
                  <td colspan="6" class="table-td text-center text-gray-500 dark:text-dark-400">{{ t('empty.noData') }}</td>
                </tr>
                <tr v-for="item in withdrawals" :key="item.id" class="border-b border-gray-100 dark:border-dark-700">
                  <td class="table-td">
                    <div class="font-mono text-sm text-gray-900 dark:text-white">#{{ item.agent_user_id }}</div>
                  </td>
                  <td class="table-td font-medium">{{ formatAmount(item.amount) }}</td>
                  <td class="table-td">
                    <div>{{ item.payment_account || '-' }}</div>
                    <a v-if="item.payment_qr_code" :href="item.payment_qr_code" target="_blank" rel="noopener" class="text-xs text-primary-600 hover:underline">{{ t('admin.agents.withdrawals.viewQr') }}</a>
                  </td>
                  <td class="table-td">
                    <span :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
                  </td>
                  <td class="table-td max-w-56 truncate">{{ item.admin_note || item.note || '-' }}</td>
                  <td class="table-td">
                    <div v-if="item.status === 'pending'" class="flex flex-wrap gap-2">
                      <button class="btn btn-primary btn-sm" @click="processWithdrawal(item.id, 'paid')">{{ t('admin.agents.actions.pay') }}</button>
                      <button class="btn btn-danger btn-sm" @click="processWithdrawal(item.id, 'rejected')">{{ t('admin.agents.actions.reject') }}</button>
                    </div>
                    <span v-else class="text-xs text-gray-500 dark:text-dark-400">{{ formatDate(item.processed_at || item.updated_at || item.created_at) }}</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </template>
        </div>
      </template>

      <template #pagination>
        <Pagination
          v-if="total > 0"
          :page="page"
          :total="total"
          :page-size="pageSize"
          @update:page="changePage"
          @update:page-size="changePageSize"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="customersDialog"
      :title="t('admin.agents.customers.title', { name: selectedProfile?.email || `#${selectedProfile?.user_id || ''}` })"
      width="extra-wide"
      @close="customersDialog = false"
    >
      <p class="mb-4 text-sm text-gray-500 dark:text-dark-400">{{ t('admin.agents.customers.hint') }}</p>
      <div v-if="customersLoading" class="flex justify-center py-10">
        <div class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
      </div>
      <div v-else-if="customers.length === 0" class="rounded-lg border border-dashed border-gray-300 p-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
        {{ t('empty.noData') }}
      </div>
      <div v-else class="overflow-x-auto">
        <table class="min-w-[700px] w-full text-left">
          <thead>
            <tr>
              <th class="table-th">{{ t('admin.agents.customers.columns.customer') }}</th>
              <th class="table-th">{{ t('admin.agents.customers.columns.usage') }}</th>
              <th class="table-th">{{ t('admin.agents.customers.columns.requests') }}</th>
              <th class="table-th">{{ t('admin.agents.customers.columns.joinedAt') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="customer in customers" :key="customer.user_id" class="border-b border-gray-100 dark:border-dark-700">
              <td class="table-td">
                <div class="font-mono text-sm text-gray-900 dark:text-white">#{{ customer.user_id }}</div>
                <div class="text-sm text-gray-700 dark:text-gray-300">{{ customer.email || '-' }}</div>
                <div class="text-xs text-gray-500 dark:text-dark-400">{{ customer.username || '-' }}</div>
              </td>
              <td class="table-td">{{ formatAmount(customer.total_usage) }}</td>
              <td class="table-td">{{ customer.request_count }}</td>
              <td class="table-td">{{ formatDate(customer.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import agentAPI, { type AgentCustomer, type AgentProfile, type AgentWithdrawal, type AgentWithdrawalStatus } from '@/api/agent'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDisplayDateTime } from '@/utils/format'

type Tab = 'profiles' | 'withdrawals'

const { t } = useI18n()
const appStore = useAppStore()
const activeTab = ref<Tab>('profiles')
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const profileKeyword = ref('')
const profiles = ref<AgentProfile[]>([])
const withdrawals = ref<AgentWithdrawal[]>([])
const withdrawalStatus = ref<AgentWithdrawalStatus | ''>('pending')
const updatingUserId = ref<number | null>(null)
const assignCustomerId = ref('')
const assignAgentId = ref('')
const assigning = ref(false)

const customersDialog = ref(false)
const customersLoading = ref(false)
const customers = ref<AgentCustomer[]>([])
const selectedProfile = ref<AgentProfile | null>(null)

function showError(error: unknown) {
  appStore.showError(extractI18nErrorMessage(error, t, 'admin.agents.errors', t('common.error')))
}

async function loadProfiles() {
  loading.value = true
  try {
    const result = await agentAPI.listAdminAgentProfiles(page.value, pageSize.value, profileKeyword.value.trim())
    profiles.value = result.items || []
    total.value = result.total || 0
  } catch (error) {
    showError(error)
  } finally {
    loading.value = false
  }
}

async function loadWithdrawals() {
  loading.value = true
  try {
    const result = await agentAPI.listAdminAgentWithdrawals(page.value, pageSize.value, withdrawalStatus.value)
    withdrawals.value = result.items || []
    total.value = result.total || 0
  } catch (error) {
    showError(error)
  } finally {
    loading.value = false
  }
}

function loadActiveTab() {
  return activeTab.value === 'profiles' ? loadProfiles() : loadWithdrawals()
}

function switchTab(tab: Tab) {
  if (activeTab.value === tab) return
  activeTab.value = tab
  page.value = 1
  total.value = 0
  void loadActiveTab()
}

function reloadProfiles() {
  page.value = 1
  void loadProfiles()
}

function reloadWithdrawals() {
  page.value = 1
  void loadWithdrawals()
}

function changePage(value: number) {
  page.value = value
  void loadActiveTab()
}

function changePageSize(value: number) {
  pageSize.value = value
  page.value = 1
  void loadActiveTab()
}

async function changeRate(profile: AgentProfile, value: string) {
  updatingUserId.value = profile.user_id
  try {
    await agentAPI.updateAdminAgentProfile({
      user_id: profile.user_id,
      enabled: profile.enabled,
      manual_rate_bps: value === 'auto' ? 0 : Number(value),
    })
    await loadProfiles()
  } catch (error) {
    showError(error)
  } finally {
    updatingUserId.value = null
  }
}

async function toggleProfile(profile: AgentProfile) {
  updatingUserId.value = profile.user_id
  try {
    await agentAPI.updateAdminAgentProfile({
      user_id: profile.user_id,
      enabled: !profile.enabled,
      manual_rate_bps: profile.manual_rate_bps,
    })
    await loadProfiles()
  } catch (error) {
    showError(error)
  } finally {
    updatingUserId.value = null
  }
}

async function assignCustomer() {
  const customerId = Number(assignCustomerId.value)
  const agentId = Number(assignAgentId.value || 0)
  if (!Number.isInteger(customerId) || customerId <= 0 || !Number.isInteger(agentId) || agentId < 0) {
    appStore.showError(t('admin.agents.assign.invalid'))
    return
  }
  assigning.value = true
  try {
    await agentAPI.assignAdminAgentCustomer({ user_id: customerId, agent_id: agentId })
    appStore.showSuccess(t('admin.agents.assign.success'))
    assignCustomerId.value = ''
    assignAgentId.value = ''
    await loadProfiles()
  } catch (error) {
    showError(error)
  } finally {
    assigning.value = false
  }
}

async function openCustomers(profile: AgentProfile) {
  selectedProfile.value = profile
  customersDialog.value = true
  customersLoading.value = true
  customers.value = []
  try {
    const result = await agentAPI.listAdminAgentCustomers(profile.user_id, 1, 100)
    customers.value = result.items || []
  } catch (error) {
    customersDialog.value = false
    showError(error)
  } finally {
    customersLoading.value = false
  }
}

async function processWithdrawal(id: number, status: Exclude<AgentWithdrawalStatus, 'pending'>) {
  try {
    await agentAPI.processAdminAgentWithdrawal(id, { status })
    appStore.showSuccess(t('admin.agents.withdrawals.processed'))
    await loadWithdrawals()
  } catch (error) {
    showError(error)
  }
}

function formatAmount(value: number | null | undefined) {
  return `$${Number(value || 0).toFixed(2)}`
}

function formatRate(value: number | null | undefined) {
  return `${(Number(value || 0) / 100).toFixed(2).replace(/\.00$/, '')}%`
}

function formatDate(value: string | number | null | undefined) {
  if (value === null || value === undefined || value === '') return '-'
  if (typeof value === 'number') return formatDisplayDateTime(new Date(value > 10_000_000_000 ? value : value * 1000).toISOString())
  return formatDisplayDateTime(value)
}

function statusLabel(status: AgentWithdrawalStatus) {
  if (status === 'pending') return t('admin.agents.status.pending')
  if (status === 'paid') return t('admin.agents.status.paid')
  if (status === 'rejected') return t('admin.agents.status.rejected')
  return t('admin.agents.status.unknown')
}

function statusClass(status: AgentWithdrawalStatus) {
  if (status === 'pending') return 'badge badge-warning'
  if (status === 'paid') return 'badge badge-success'
  if (status === 'rejected') return 'badge badge-danger'
  return 'badge'
}

onMounted(() => {
  void loadProfiles()
})
</script>

<style scoped>
.table-th {
  @apply whitespace-nowrap border-b border-gray-200 bg-gray-50/80 px-5 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-600 dark:border-dark-700 dark:bg-dark-800/80 dark:text-dark-300;
}

.table-td {
  @apply whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300;
}

.label-text {
  @apply mb-1 block text-xs font-medium text-gray-600 dark:text-dark-300;
}
</style>
