<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  bytesWritten: number
  totalBytes: number
  speed: number
  compact?: boolean
}>()

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatDuration(seconds: number): string {
  if (seconds < 60) return Math.round(seconds) + 's'
  if (seconds < 3600) return Math.floor(seconds / 60) + 'm ' + Math.round(seconds % 60) + 's'
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return h + 'h ' + m + 'm'
}

const progress = computed(() => {
  if (props.totalBytes === 0) return -1
  return Math.round(props.bytesWritten / props.totalBytes * 100)
})

const elapsed = computed(() => {
  if (props.speed <= 0) return -1
  return props.bytesWritten / props.speed
})

const eta = computed(() => {
  if (props.speed <= 0 || props.totalBytes <= 0) return -1
  return (props.totalBytes - props.bytesWritten) / props.speed
})

const rightLabel = computed(() => {
  const parts: string[] = []
  if (progress.value >= 0) parts.push(progress.value + '%')
  if (elapsed.value > 0) parts.push(formatDuration(elapsed.value))
  if (eta.value > 0) parts.push('(' + formatDuration(eta.value) + ' left)')
  if (props.speed > 0) parts.push(formatBytes(props.speed) + '/s')
  return parts.join(' ')
})
</script>

<template>
  <div>
    <div class="w-full bg-gray-200 rounded-full" :class="compact ? 'h-1.5' : 'h-2'">
      <div
        v-if="progress >= 0"
        class="bg-blue-600 rounded-full transition-all duration-300"
        :class="compact ? 'h-1.5' : 'h-2'"
        :style="{ width: progress + '%' }"
      ></div>
      <div
        v-else
        class="bg-blue-500 rounded-full animate-pulse"
        :class="compact ? 'h-1.5' : 'h-2'"
        style="width: 100%"
      ></div>
    </div>
    <div class="flex justify-between text-gray-500 mt-1" :class="compact ? 'text-xs' : 'text-sm'">
      <span v-if="totalBytes > 0">
        {{ formatBytes(bytesWritten) }} / {{ formatBytes(totalBytes) }}
      </span>
      <span v-else>
        {{ formatBytes(bytesWritten) }} downloaded
      </span>
      <span>{{ rightLabel }}</span>
    </div>
  </div>
</template>
