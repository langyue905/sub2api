<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import CanvasWorkbenchView from '@/views/user/CanvasWorkbenchView.vue'

const authStore = useAuthStore()
const route = useRoute()
const isCanvasWorkbench = computed(() => route.name === 'CanvasWorkbench')
const currentUserId = computed(() => authStore.isAuthenticated ? authStore.user?.id ?? null : null)
const canvasWorkbenchUserId = ref<number | null>(null)

watch(
  [isCanvasWorkbench, currentUserId],
  ([active, userId]) => {
    if (canvasWorkbenchUserId.value !== userId) {
      canvasWorkbenchUserId.value = null
    }
    if (active && userId !== null) {
      canvasWorkbenchUserId.value = userId
    }
  },
  { immediate: true, flush: 'sync' }
)
</script>

<template>
  <RouterView v-slot="{ Component, route: resolvedRoute }">
    <component :is="Component" v-if="resolvedRoute.name !== 'CanvasWorkbench'" />
  </RouterView>
  <!-- Keep the iframe in the same DOM position. Moving it with KeepAlive reloads its document. -->
  <div
    v-if="canvasWorkbenchUserId !== null"
    v-show="isCanvasWorkbench"
    :key="canvasWorkbenchUserId"
  >
    <CanvasWorkbenchView />
  </div>
</template>
