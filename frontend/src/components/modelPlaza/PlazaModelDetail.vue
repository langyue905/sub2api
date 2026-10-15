<template>
  <BaseDialog :show="!!model" :title="model?.name || ''" width="extra-wide" close-on-click-outside @close="emit('close')">
    <div v-if="model" class="space-y-5">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-200">{{ model.profile?.summary || t('modelPlaza.cards.noSummary') }}</p>
      <div v-if="model.profile?.tags?.length" class="flex flex-wrap gap-2">
        <span v-for="tag in model.profile.tags" :key="tag" class="rounded-lg bg-primary-50 px-2.5 py-1 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ tag }}</span>
      </div>
      <dl v-if="model.profile?.specs?.length" class="grid gap-px overflow-hidden rounded-xl border border-gray-200 bg-gray-200 dark:border-dark-700 dark:bg-dark-700 sm:grid-cols-2">
        <div v-for="(spec, index) in model.profile.specs" :key="index" class="bg-gray-50 p-3 dark:bg-dark-800">
          <dt class="text-xs text-gray-500">{{ spec.label }}</dt>
          <dd class="mt-1 whitespace-pre-wrap break-words text-sm text-gray-900 dark:text-white">{{ spec.value }}</dd>
        </div>
      </dl>
      <div v-if="descriptionHtml" class="model-description text-sm text-gray-700 dark:text-dark-200" v-html="descriptionHtml"></div>
      <div>
        <h4 class="mb-3 font-semibold text-gray-900 dark:text-white">{{ t('modelPlaza.cards.pricingDetails') }}</h4>
        <PlazaModelPricingTable
          :models="[model]" :platform="group.platform"
          :rate-multiplier="group.rate_multiplier" :user-rate-multiplier="group.user_rate_multiplier"
          :image-rate-independent="group.image_rate_independent" :image-rate-multiplier="group.image_rate_multiplier"
          :video-rate-independent="group.video_rate_independent" :video-rate-multiplier="group.video_rate_multiplier"
          :peak-window="peakWindow" :peak-rate-multiplier="group.peak_rate_multiplier"
        />
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlazaModelPricingTable from './PlazaModelPricingTable.vue'
import type { ModelPlazaGroup, PlazaModel } from '@/api/modelPlaza'
const props = defineProps<{ model: PlazaModel | null; group: ModelPlazaGroup; peakWindow?: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const descriptionHtml = computed(() => DOMPurify.sanitize(marked.parse(props.model?.profile?.description || '') as string))
</script>

<style scoped>
.model-description { line-height: 1.8; overflow-wrap: anywhere; }
.model-description :deep(p), .model-description :deep(ul), .model-description :deep(ol) { margin-bottom: 0.75rem; }
.model-description :deep(h1), .model-description :deep(h2), .model-description :deep(h3) { margin: 1rem 0 0.5rem; font-weight: 600; }
.model-description :deep(ul) { list-style: disc; padding-left: 1.25rem; }
.model-description :deep(ol) { list-style: decimal; padding-left: 1.25rem; }
.model-description :deep(a) { text-decoration: underline; color: var(--color-primary-600, #0d9488); }
.model-description :deep(pre) { overflow-x: auto; padding: 0.75rem; background: rgb(128 128 128 / 10%); border-radius: 0.5rem; }
.model-description :deep(img) { max-width: 100%; }
.model-description :deep(table) { display: block; max-width: 100%; overflow-x: auto; }
.model-description :deep(th), .model-description :deep(td) { border: 1px solid rgb(128 128 128 / 25%); padding: 0.4rem 0.75rem; }
</style>
