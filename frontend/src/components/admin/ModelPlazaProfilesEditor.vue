<template>
  <section class="space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t(`${key}.title`) }}</h3>
        <p class="mt-1 max-w-2xl text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t(`${key}.hint`) }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="profiles.length >= 500" @click="addProfile">
        <Icon name="plus" size="sm" />{{ t(`${key}.add`) }}
      </button>
    </div>
    <p v-if="!profiles.length" class="rounded-lg bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-800">{{ t(`${key}.empty`) }}</p>
    <article v-for="(profile, index) in profiles" :key="index" class="rounded-xl border border-gray-200 dark:border-dark-700">
      <div class="flex items-center gap-2 p-3">
        <button type="button" class="flex min-w-0 flex-1 items-center gap-2 text-left text-sm font-medium text-gray-800 dark:text-dark-100" :aria-expanded="expanded === index" @click="expanded = expanded === index ? null : index">
          <Icon :name="expanded === index ? 'chevronDown' : 'chevronRight'" size="sm" class="shrink-0" />
          <span class="truncate">{{ profile.model || t(`${key}.newModel`) }}</span>
          <span v-if="profile.platform" class="shrink-0 text-xs font-normal text-gray-400">{{ profile.platform }}</span>
        </button>
        <button type="button" class="rounded p-2 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20" :title="t(`${key}.remove`)" :aria-label="t(`${key}.remove`)" @click="removeProfile(index)"><Icon name="trash" size="sm" /></button>
      </div>
      <div v-if="expanded === index" class="space-y-4 border-t border-gray-100 p-4 dark:border-dark-700">
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm text-gray-700 dark:text-dark-200">
            {{ t(`${key}.model`) }}
            <input :value="profile.model" class="input mt-1 font-mono text-sm" required maxlength="200" :placeholder="t(`${key}.modelPlaceholder`)" @input="updateProfile(index, { model: inputText($event) })" />
          </label>
          <label class="block text-sm text-gray-700 dark:text-dark-200">
            {{ t(`${key}.platform`) }}
            <input :value="profile.platform" class="input mt-1 text-sm" maxlength="64" :placeholder="t(`${key}.platformPlaceholder`)" @input="updateProfile(index, { platform: inputText($event) })" />
          </label>
        </div>
        <label class="block text-sm text-gray-700 dark:text-dark-200">
          {{ t(`${key}.summary`) }}
          <textarea :value="profile.summary" class="input mt-1 text-sm" rows="2" maxlength="500" @input="updateProfile(index, { summary: inputText($event) })"></textarea>
        </label>
        <label class="block text-sm text-gray-700 dark:text-dark-200">
          {{ t(`${key}.tags`) }}
          <input :value="profile.tags.join(', ')" class="input mt-1 text-sm" :placeholder="t(`${key}.tagsPlaceholder`)" @change="updateProfile(index, { tags: inputText($event).split(/[,，]/).map(s => s.trim()).filter(Boolean) })" />
        </label>
        <label class="block text-sm text-gray-700 dark:text-dark-200">
          {{ t(`${key}.cover`) }}
          <input :value="profile.cover_url" type="url" class="input mt-1 text-sm" placeholder="https://…" @input="updateProfile(index, { cover_url: inputText($event) })" />
        </label>
        <label class="block text-sm text-gray-700 dark:text-dark-200">
          {{ t(`${key}.description`) }}
          <textarea :value="profile.description" class="input mt-1 font-mono text-sm" rows="7" maxlength="8000" @input="updateProfile(index, { description: inputText($event) })"></textarea>
        </label>
        <div class="space-y-2">
          <p class="text-sm text-gray-700 dark:text-dark-200">{{ t(`${key}.specs`) }}</p>
          <div v-for="(spec, specIndex) in profile.specs" :key="specIndex" class="flex flex-wrap gap-2 sm:flex-nowrap">
            <input :value="spec.label" class="input min-w-0 flex-1 text-sm sm:max-w-[12rem]" :aria-label="t(`${key}.specLabel`)" :placeholder="t(`${key}.specLabel`)" @input="updateSpec(index, specIndex, 'label', inputText($event))" />
            <input :value="spec.value" class="input min-w-0 flex-1 text-sm" :aria-label="t(`${key}.specValue`)" :placeholder="t(`${key}.specValue`)" @input="updateSpec(index, specIndex, 'value', inputText($event))" />
            <button type="button" class="p-2 text-red-500" :aria-label="t(`${key}.removeSpec`)" @click="updateProfile(index, { specs: profile.specs.filter((_, i) => i !== specIndex) })"><Icon name="x" size="sm" /></button>
          </div>
          <button type="button" class="text-xs text-primary-600 dark:text-primary-400" :disabled="profile.specs.length >= 16" @click="updateProfile(index, { specs: [...profile.specs, { label: '', value: '' }] })">{{ t(`${key}.addSpec`) }}</button>
        </div>
      </div>
    </article>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PlazaModelProfile } from '@/api/modelPlaza'

const profiles = defineModel<PlazaModelProfile[]>({ required: true })
const expanded = ref<number | null>(null)
const { t } = useI18n()
const key = 'admin.settings.features.modelPlaza.profiles'
const inputText = (event: Event) => (event.target as HTMLInputElement).value
function updateProfile(index: number, update: Partial<PlazaModelProfile>) {
  profiles.value = profiles.value.map((profile, i) => i === index ? { ...profile, ...update } : profile)
}
function addProfile() {
  expanded.value = profiles.value.length
  profiles.value = [...profiles.value, { model: '', platform: '', summary: '', description: '', cover_url: '', tags: [], specs: [] }]
}
function removeProfile(index: number) {
  profiles.value = profiles.value.filter((_, i) => i !== index)
  if (expanded.value === index) expanded.value = null
  else if (expanded.value != null && expanded.value > index) expanded.value--
}
function updateSpec(index: number, specIndex: number, field: 'label' | 'value', value: string) {
  updateProfile(index, { specs: profiles.value[index].specs.map((spec, i) => i === specIndex ? { ...spec, [field]: value } : spec) })
}
</script>
