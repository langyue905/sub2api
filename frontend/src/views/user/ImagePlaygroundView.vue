<template>
  <AppLayout>
    <div class="image-playground-layout">
      <div class="image-playground-toolbar">
        <h1 class="min-w-0 truncate text-base font-semibold text-gray-900 dark:text-white">
          {{ t('nav.imagePlayground') }}
        </h1>
        <a
          :href="playgroundUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white"
          :title="t('customPage.openInNewTab')"
          :aria-label="t('customPage.openInNewTab')"
        >
          <Icon name="externalLink" size="sm" :stroke-width="2" />
        </a>
      </div>

      <iframe
        :src="playgroundUrl"
        class="image-playground-frame"
        :title="t('nav.imagePlayground')"
        allow="clipboard-read; clipboard-write"
      ></iframe>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const playgroundUrl = '/playgrounds/image/'
</script>

<style scoped>
.image-playground-layout {
  display: flex;
  height: calc(100vh - 64px - 4rem);
  min-height: 520px;
  flex-direction: column;
  overflow: hidden;
  border-radius: 0.75rem;
  background: #fff;
  box-shadow: 0 1px 3px rgb(15 23 42 / 8%);
}

.dark .image-playground-layout {
  background: #111827;
}

.image-playground-toolbar {
  display: flex;
  min-height: 3.25rem;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  border-bottom: 1px solid rgb(229 231 235);
  padding: 0.625rem 0.875rem 0.625rem 1rem;
}

.dark .image-playground-toolbar {
  border-color: rgb(55 65 81);
}

.image-playground-frame {
  display: block;
  min-height: 0;
  width: 100%;
  flex: 1 1 auto;
  border: 0;
  background: #f9fafb;
}

.dark .image-playground-frame {
  background: #030712;
}

@media (max-width: 640px) {
  .image-playground-layout {
    min-height: 460px;
    border-radius: 0.5rem;
  }
}
</style>
