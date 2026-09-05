<template>
  <section class="relative z-10 mb-4 mt-6 rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-900">
    <div class="mb-3 text-sm font-medium text-gray-900 dark:text-gray-100">手动补绑定邀请关系</div>
    <div class="flex flex-wrap items-end gap-3">
      <label class="text-sm text-gray-600 dark:text-gray-300">
        被邀请用户 ID
        <input v-model.number="inviteeId" type="number" min="1" class="input mt-1 w-40" />
      </label>
      <label class="text-sm text-gray-600 dark:text-gray-300">
        邀请人用户 ID
        <input v-model.number="inviterId" type="number" min="1" class="input mt-1 w-40" />
      </label>
      <button class="btn btn-primary" :disabled="saving || !inviteeId || !inviterId" @click="bind">
        {{ saving ? '绑定中...' : '绑定' }}
      </button>
    </div>
  </section>
  <AdminAffiliateRecordsTable type="invites" />
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { affiliatesAPI } from '@/api/admin/affiliates'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import AdminAffiliateRecordsTable from './AdminAffiliateRecordsTable.vue'

const appStore = useAppStore()
const { t } = useI18n()
const inviteeId = ref<number | null>(null)
const inviterId = ref<number | null>(null)
const saving = ref(false)

async function bind() {
  if (!inviteeId.value || !inviterId.value) return
  saving.value = true
  try {
    await affiliatesAPI.bindInviter(inviteeId.value, inviterId.value)
    appStore.showSuccess('邀请关系已绑定')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.affiliates.errors', '绑定失败'))
  } finally {
    saving.value = false
  }
}
</script>
