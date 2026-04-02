<script setup lang="ts">
import { useDownloadStream } from '../composables/useDownloadStream'
import DownloadProgressBar from './DownloadProgressBar.vue'

const { downloads } = useDownloadStream()

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

        <DownloadProgressBar
          :bytes-written="download.bytesWritten"
          :total-bytes="download.totalBytes"
          :speed="download.speed"
        />
      </div>
    </div>
  </div>
</template>
