<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'

interface File {
  id: string
  fileName: string
  fileSize: number
  status: 'available' | 'downloading' | 'downloaded' | 'failed' | 'skipped' | 'deleted' | 'cancelled'
  sourceId: string
  productId: string
  releasedAt?: string
  localPath?: string
  skipped: boolean
  errorMessage?: string
}

const props = defineProps<{
  productId: string | null
}>()

const emit = defineEmits<{
  (e: 'files-changed'): void
}>()

const files = ref<File[]>([])
const total = ref(0)
const loading = ref(false)
const statusFilter = ref<string>('')
const offset = ref(0)
const limit = 50

async function fetchFiles(showLoading = true) {
  // Only show loading state on initial load, not background refreshes
  if (showLoading && files.value.length === 0) {
    loading.value = true
  }

  const params = new URLSearchParams()
  if (props.productId) params.set('productId', props.productId)
  if (statusFilter.value) params.set('status', statusFilter.value)
  params.set('offset', offset.value.toString())
  params.set('limit', limit.toString())

  try {
    const response = await fetch(`/api/files?${params}`, { credentials: 'include' })
    if (response.ok) {
      const data = await response.json()
      // Update files in-place to preserve scroll position and avoid flicker
      updateFilesInPlace(data.files)
      total.value = data.total
    }
  } catch (error) {
    console.error('Failed to fetch files:', error)
  }

  loading.value = false
}

// Update files array in-place to minimize DOM changes
function updateFilesInPlace(newFiles: File[]) {
  const newMap = new Map(newFiles.map(f => [f.id, f]))

  // Check if the file list structure changed (different files or order)
  const structureChanged = files.value.length !== newFiles.length ||
    files.value.some((f, i) => newFiles[i]?.id !== f.id)

  if (structureChanged) {
    // Structure changed, replace the array
    files.value = newFiles
  } else {
    // Same structure, update properties in-place
    for (let i = 0; i < files.value.length; i++) {
      const newFile = newMap.get(files.value[i].id)
      if (newFile && JSON.stringify(files.value[i]) !== JSON.stringify(newFile)) {
        Object.assign(files.value[i], newFile)
      }
    }
  }
}

async function downloadFile(fileId: string) {
  try {
    await fetch(`/api/files/${encodeURIComponent(fileId)}/download`, {
      method: 'POST',
      credentials: 'include',
    })
    emit('files-changed')
    fetchFiles()
  } catch (error) {
    console.error('Failed to start download:', error)
  }
}

async function toggleSkip(file: File) {
  try {
    const method = file.skipped ? 'DELETE' : 'PUT'
    await fetch(`/api/files/${encodeURIComponent(file.id)}/skip`, { method, credentials: 'include' })
    emit('files-changed')
    fetchFiles()
  } catch (error) {
    console.error('Failed to toggle skip:', error)
  }
}

async function deleteFile(fileId: string) {
  if (!confirm('Delete this file from disk?')) return
  try {
    await fetch(`/api/files/${encodeURIComponent(fileId)}`, {
      method: 'DELETE',
      credentials: 'include',
    })
    emit('files-changed')
    fetchFiles()
  } catch (error) {
    console.error('Failed to delete file:', error)
  }
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '-'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatDate(dateStr?: string): string {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleDateString()
}

function getStatusBadgeClass(status: string): string {
  const classes: Record<string, string> = {
    available: 'bg-gray-100 text-gray-800',
    downloading: 'bg-blue-100 text-blue-800',
    downloaded: 'bg-green-100 text-green-800',
    failed: 'bg-red-100 text-red-800',
    skipped: 'bg-yellow-100 text-yellow-800',
    deleted: 'bg-orange-100 text-orange-800',
    cancelled: 'bg-purple-100 text-purple-800',
  }
  return classes[status] || 'bg-gray-100 text-gray-800'
}

watch(() => props.productId, () => {
  offset.value = 0
  fetchFiles()
  startRefreshInterval()
})

watch(statusFilter, () => {
  offset.value = 0
  fetchFiles()
})

let refreshInterval: number | null = null

function hasActiveDownloads(): boolean {
  return files.value.some(f => f.status === 'downloading')
}

function startRefreshInterval() {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
  // 5s when no files or active downloads, 30s otherwise
  const interval = (files.value.length === 0 || hasActiveDownloads()) ? 5000 : 30000
  refreshInterval = window.setInterval(() => {
    const hadActiveDownloads = hasActiveDownloads()
    fetchFiles(false)
    // Adjust interval if state changed (files appeared or downloads completed)
    if ((files.value.length > 0 && interval === 5000 && !hasActiveDownloads()) ||
        (hadActiveDownloads && !hasActiveDownloads())) {
      startRefreshInterval()
    }
  }, interval)
}

onMounted(() => {
  fetchFiles()
  startRefreshInterval()
})

onUnmounted(() => {
  if (refreshInterval) clearInterval(refreshInterval)
})
</script>

<template>
  <div class="bg-white rounded-lg shadow">
    <!-- Filters -->
    <div class="p-4 border-b flex items-center space-x-4">
      <select
        v-model="statusFilter"
        class="px-3 py-2 border border-gray-300 rounded-md text-sm"
      >
        <option value="">All Status</option>
        <option value="available">Available</option>
        <option value="downloading">Downloading</option>
        <option value="downloaded">Downloaded</option>
        <option value="failed">Failed</option>
        <option value="cancelled">Cancelled</option>
        <option value="skipped">Skipped</option>
      </select>

      <span class="text-sm text-gray-500">
        {{ total }} files
      </span>

      <button
        @click="() => fetchFiles()"
        class="ml-auto text-sm text-blue-600 hover:text-blue-800"
      >
        Refresh
      </button>
    </div>

    <!-- File Table -->
    <div class="overflow-x-auto">
      <table class="w-full">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">File</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Size</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Released</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Status</th>
            <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 uppercase">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          <tr v-if="loading" class="text-center">
            <td colspan="5" class="px-4 py-8 text-gray-500">Loading...</td>
          </tr>
          <tr v-else-if="files.length === 0" class="text-center">
            <td colspan="5" class="px-4 py-8 text-gray-500">
              <div class="flex items-center justify-center gap-2">
                <svg class="animate-spin h-4 w-4 text-blue-500" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                  <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                  <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                </svg>
                Syncing files... (first sync may take a while)
              </div>
            </td>
          </tr>
          <tr v-else v-for="file in files" :key="file.id" class="hover:bg-gray-50">
            <td class="px-4 py-3">
              <div class="font-medium text-gray-900 truncate max-w-xs" :title="file.fileName">
                {{ file.fileName }}
              </div>
            </td>
            <td class="px-4 py-3 text-sm text-gray-500">
              {{ formatBytes(file.fileSize) }}
            </td>
            <td class="px-4 py-3 text-sm text-gray-500">
              {{ formatDate(file.releasedAt) }}
            </td>
            <td class="px-4 py-3">
              <span
                class="px-2 py-1 text-xs rounded-full cursor-default"
                :class="getStatusBadgeClass(file.status)"
                :title="file.errorMessage || ''"
              >
                {{ file.status }}
              </span>
              <div
                v-if="file.status === 'failed' && file.errorMessage"
                class="text-xs text-red-600 mt-1 cursor-help"
                :title="file.errorMessage"
              >
                <span class="underline decoration-dotted">{{ file.errorMessage.length > 50 ? file.errorMessage.substring(0, 50) + '...' : file.errorMessage }}</span>
              </div>
            </td>
            <td class="px-4 py-3 text-right space-x-2">
              <button
                v-if="file.status === 'available' || file.status === 'failed' || file.status === 'cancelled'"
                @click="downloadFile(file.id)"
                class="text-sm text-blue-600 hover:text-blue-800"
              >
                {{ file.status === 'failed' || file.status === 'cancelled' ? 'Retry' : 'Download' }}
              </button>
              <button
                v-if="file.status === 'downloaded' && file.localPath"
                class="text-sm text-green-600 hover:text-green-800"
                :title="file.localPath"
              >
                Open
              </button>
              <button
                v-if="file.status === 'downloaded'"
                @click="deleteFile(file.id)"
                class="text-sm text-red-600 hover:text-red-800"
              >
                Delete
              </button>
              <button
                v-if="file.status !== 'downloaded'"
                @click="toggleSkip(file)"
                class="text-sm text-gray-600 hover:text-gray-800"
              >
                {{ file.skipped ? 'Unskip' : 'Skip' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Pagination -->
    <div v-if="total > limit" class="p-4 border-t flex justify-between items-center">
      <button
        @click="offset = Math.max(0, offset - limit); fetchFiles()"
        :disabled="offset === 0"
        class="px-3 py-1 text-sm border rounded disabled:opacity-50"
      >
        Previous
      </button>
      <span class="text-sm text-gray-500">
        {{ offset + 1 }} - {{ Math.min(offset + limit, total) }} of {{ total }}
      </span>
      <button
        @click="offset = offset + limit; fetchFiles()"
        :disabled="offset + limit >= total"
        class="px-3 py-1 text-sm border rounded disabled:opacity-50"
      >
        Next
      </button>
    </div>
  </div>
</template>
