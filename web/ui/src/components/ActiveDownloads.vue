<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

interface DownloadProgress {
  fileId: string
  fileName: string
  bytesWritten: number
  totalBytes: number
  speed: number
}

const downloads = ref<DownloadProgress[]>([])
let eventSource: EventSource | null = null

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatSpeed(bytesPerSecond: number): string {
  return formatBytes(bytesPerSecond) + '/s'
}

function getProgress(download: DownloadProgress): number {
  if (download.totalBytes === 0) return 0
  return Math.round((download.bytesWritten / download.totalBytes) * 100)
}

async function cancelDownload(fileId: string) {
  try {
    await fetch(`/api/files/${encodeURIComponent(fileId)}/cancel`, {
      method: 'POST',
      credentials: 'include',
    })
  } catch (error) {
    console.error('Failed to cancel download:', error)
  }
}

function connectSSE() {
  eventSource = new EventSource('/api/downloads/active')

  eventSource.onmessage = (event) => {
    try {
      downloads.value = JSON.parse(event.data)
    } catch {
      // Ignore parse errors
    }
  }

  eventSource.onerror = () => {
    eventSource?.close()
    setTimeout(connectSSE, 5000)
  }
}

onMounted(() => {
  connectSSE()
})

onUnmounted(() => {
  eventSource?.close()
})
</script>

<template>
  <div class="bg-white rounded-lg shadow">
    <div v-if="downloads.length === 0" class="p-4 text-gray-500 text-center">
      No active downloads
    </div>

    <div v-else class="divide-y">
      <div
        v-for="download in downloads"
        :key="download.fileId"
        class="p-4"
      >
        <div class="flex justify-between items-center mb-2">
          <span class="font-medium text-gray-900 truncate">{{ download.fileName }}</span>
          <button
            @click="cancelDownload(download.fileId)"
            class="text-sm text-red-600 hover:text-red-800"
          >
            Cancel
          </button>
        </div>

        <div class="w-full bg-gray-200 rounded-full h-2 mb-2">
          <div
            class="bg-blue-600 h-2 rounded-full transition-all duration-300"
            :style="{ width: getProgress(download) + '%' }"
          ></div>
        </div>

        <div class="flex justify-between text-sm text-gray-500">
          <span>{{ formatBytes(download.bytesWritten) }} / {{ formatBytes(download.totalBytes) }}</span>
          <span>{{ getProgress(download) }}% - {{ formatSpeed(download.speed) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
