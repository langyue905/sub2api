<template>
    <div class="canvas-workbench-layout">
      <iframe
        :src="workbenchUrl"
        class="canvas-workbench-frame"
        :title="t('nav.canvasWorkbench')"
        allow="clipboard-read; clipboard-write"
      ></iframe>
    </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const workbenchUrl = '/playgrounds/canvas/'
</script>

<style scoped>
.canvas-workbench-layout {
  display: flex;
  position: fixed;
  inset: 0;
  height: 100dvh;
  flex-direction: column;
  overflow: hidden;
  background: #fff;
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

</style>
