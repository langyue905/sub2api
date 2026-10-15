<template>
  <article class="flex h-full flex-col overflow-hidden rounded-xl border border-gray-200 bg-white transition-shadow hover:shadow-md dark:border-dark-700 dark:bg-dark-800">
    <div class="relative flex h-28 items-center justify-between overflow-hidden bg-gradient-to-br from-primary-50 via-gray-50 to-primary-100 px-5 dark:from-dark-700 dark:via-dark-800 dark:to-primary-950">
      <img v-if="coverUrl" :src="coverUrl" alt="" loading="lazy" referrerpolicy="no-referrer" class="absolute inset-0 h-full w-full object-cover" @error="coverFailed = true" />
      <span class="relative rounded-xl bg-white/90 p-3 text-primary-600 shadow-sm dark:bg-dark-900/90 dark:text-primary-300">
        <Icon :name="mediaIcon" size="lg" />
      </span>
      <span class="relative rounded-full bg-white/90 px-2.5 py-1 text-xs text-gray-600 dark:bg-dark-900/90 dark:text-dark-200">{{ typeLabel }}</span>
    </div>
    <div class="flex flex-1 flex-col gap-3 p-4">
      <div>
        <button type="button" class="break-all text-left text-base font-semibold text-gray-900 hover:text-primary-600 dark:text-white" @click="emit('select', model)">{{ model.name }}</button>
        <p class="mt-1 text-xs text-gray-400">{{ model.platform }}</p>
      </div>
      <p class="line-clamp-3 min-h-[3.75rem] text-sm leading-5 text-gray-500 dark:text-dark-300">{{ model.profile?.summary || t('modelPlaza.cards.noSummary') }}</p>
      <div v-if="model.profile?.tags?.length" class="flex flex-wrap gap-1.5">
        <span v-for="tag in model.profile.tags" :key="tag" class="rounded-md bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ tag }}</span>
      </div>
      <div class="mt-auto space-y-1.5 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900/70">
        <p class="mb-2 font-medium text-gray-500 dark:text-dark-400">{{ t('modelPlaza.cards.basePrice') }}</p>
        <template v-if="isToken && baseToken">
          <div v-for="kind in (['input', 'output'] as const)" :key="kind" class="flex justify-between gap-2">
            <span class="text-gray-500">{{ t('modelPlaza.table.' + kind) }}</span>
            <span class="font-mono text-primary-700 dark:text-primary-300">{{ price(baseToken[kind === 'input' ? 'input_price' : 'output_price'], 1_000_000) }} {{ t(unitKey) }}</span>
          </div>
        </template>
        <template v-else-if="mediaTiers.length">
          <div v-for="(tier, index) in mediaTiers" :key="index" class="flex justify-between gap-2">
            <span class="text-gray-500">{{ tier.tier_label || t('modelPlaza.cards.tier', { n: index + 1 }) }}</span>
            <span class="font-mono text-primary-700 dark:text-primary-300">{{ price(tier.per_request_price) }} {{ t(unitKey) }}</span>
          </div>
        </template>
        <p v-else-if="model.pricing?.per_request_price != null" class="font-mono text-primary-700 dark:text-primary-300">{{ price(model.pricing.per_request_price) }} {{ t(unitKey) }}</p>
        <p v-else class="text-gray-400">{{ t('modelPlaza.detail.noPricing') }}</p>
        <p v-if="model.time_pricing?.periods.length" class="pt-1 text-amber-600 dark:text-amber-400">{{ t('modelPlaza.cards.timePricing') }}</p>
      </div>
      <button type="button" class="btn btn-secondary w-full justify-center text-sm" @click="emit('select', model)">{{ t('modelPlaza.cards.viewDetails') }} <Icon name="chevronRight" size="sm" /></button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ModelPlazaGroup, PlazaModel } from '@/api/modelPlaza'
import { formatScaled } from '@/utils/pricing'
import { sanitizeUrl } from '@/utils/url'
import { plazaBaseTokenPrices, plazaModelRate, plazaUnitKey } from '@/utils/modelPlaza'

const props = defineProps<{ model: PlazaModel; group: ModelPlazaGroup }>()
const emit = defineEmits<{ select: [model: PlazaModel] }>()
const { t } = useI18n()
const coverFailed = ref(false)
watch(() => props.model.profile?.cover_url, () => { coverFailed.value = false })
const coverUrl = computed(() => coverFailed.value ? '' : sanitizeUrl(props.model.profile?.cover_url || ''))
const mode = computed(() => props.model.pricing?.billing_mode || 'token')
const isToken = computed(() => mode.value === 'token')
const mediaIcon = computed(() => mode.value === 'video' ? 'play' : mode.value === 'image' ? 'photo' : 'sparkles')
const typeLabel = computed(() => t(mode.value === 'video' ? 'modelPlaza.table.perSecond' : mode.value === 'image' ? 'modelPlaza.table.perImage' : mode.value === 'per_request' ? 'modelPlaza.table.perRequest' : 'modelPlaza.cards.tokenModel'))
const unitKey = computed(() => plazaUnitKey(props.model))
const baseToken = computed(() => plazaBaseTokenPrices(props.model))
const mediaTiers = computed(() => (props.model.pricing?.intervals || []).filter(tier => tier.per_request_price != null))
function price(value: number | null | undefined, scale = 1) {
  return value == null ? '-' : formatScaled(value * plazaModelRate(props.model, props.group), scale, 2)
}
</script>
