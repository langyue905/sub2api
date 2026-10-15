<template>
  <AppLayout>
    <div class="canvas-workbench-layout">
      <iframe
        :src="workbenchUrl"
        class="canvas-workbench-frame"
        :title="t('nav.canvasWorkbench')"
        allow="clipboard-read; clipboard-write"
      ></iframe>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'

const { t } = useI18n()
const workbenchUrl = '/playgrounds/canvas/'
</script>

<style scoped>
.canvas-workbench-layout {
  display: flex;
  height: calc(100dvh - 64px - 4rem);
  min-height: 320px;
  flex-direction: column;
  overflow: hidden;
  border-radius: 0.75rem;
  background: #fff;
  box-shadow: 0 1px 3px rgb(15 23 42 / 8%);
}

.dark .canvas-workbench-layout {
  background: #111827;
}

.canvas-workbench-frame {
  display: block;
  min-height: 0;
  width: 100%;
  flex: 1 1 auto;
  border: 0;
  background: #f9fafb;
}

.dark .canvas-workbench-frame {
  background: #030712;
}

@media (max-width: 1023px) {
  .canvas-workbench-layout {
    height: calc(100dvh - 64px - 3rem);
  }
}

@media (max-width: 767px) {
  .canvas-workbench-layout {
    height: calc(100dvh - 64px - 2rem);
    border-radius: 0.5rem;
  }
}
</style>
