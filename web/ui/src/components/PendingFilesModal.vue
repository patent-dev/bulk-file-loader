<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

interface PendingFile {
  id: string
  fileName: string
  fileSize?: number
  productId?: string
  productName?: string
}

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'files-changed'): void
}>()

const files = ref<PendingFile[]>([])
const loading = ref(true)
const downloading = ref<Set<string>>(new Set())

// Group files by product
const groupedFiles = computed(() => {
  const groups: Record<string, PendingFile[]> = {}
  for (const file of files.value) {
    const productName = file.productName || 'Unknown Product'
    if (!groups[productName]) {
      groups[productName] = []
    }
    groups[productName].push(file)
  }
  return groups
})

function formatFileSize(bytes?: number): string {
  if (!bytes) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

async function fetchPendingFiles() {
  loading.value = true
  try {
    // Get products first to map product IDs to names and filter by auto-download
    const productsRes = await fetch('/api/products', { credentials: 'include' })
    const products = productsRes.ok ? await productsRes.json() : []
    const autoDownloadProductIds = new Set(
      products.filter((p: { autoDownload: boolean }) => p.autoDownload).map((p: { id: string }) => p.id)
    )
    const productMap = new Map(products.map((p: { id: string; name: string }) => [p.id, p.name]))

    // Get files with status=available and filter to only auto-download products
    const filesRes = await fetch('/api/files?status=available&limit=500', { credentials: 'include' })
    if (filesRes.ok) {
      const data = await filesRes.json()
      files.value = (data.files || [])
        .filter((f: PendingFile) => f.productId && autoDownloadProductIds.has(f.productId))
        .map((f: PendingFile) => ({
          ...f,
          productName: f.productId ? productMap.get(f.productId) || f.productId : 'Unknown'
        }))
    }
  } catch (error) {
    console.error('Failed to fetch pending files:', error)
  }
  loading.value = false
}

async function downloadFile(fileId: string) {
  downloading.value.add(fileId)
  try {
    await fetch(`/api/files/${encodeURIComponent(fileId)}/download`, {
      method: 'POST',
      credentials: 'include',
    })
    emit('files-changed')
    // Refresh the list to remove downloaded file
    await fetchPendingFiles()
  } catch (error) {
    console.error('Failed to start download:', error)
  }
  downloading.value.delete(fileId)
}

async function downloadAll() {
  for (const file of files.value) {
    await downloadFile(file.id)
  }
}

onMounted(() => {
  fetchPendingFiles()
})
</script>

<template>
  <Teleport to="body">
    <div
      class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
      @click="emit('close')"
    >
      <div
        class="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[80vh] flex flex-col"
        @click.stop
      >
        <!-- Header -->
        <div class="p-4 border-b flex items-center justify-between">
          <h2 class="text-lg font-medium">Pending Files</h2>
          <div class="flex items-center gap-3">
            <button
              v-if="files.length > 0"
              @click="downloadAll"
              class="px-3 py-1.5 text-sm bg-blue-600 text-white rounded hover:bg-blue-700"
            >
              Download All ({{ files.length }})
            </button>
            <button
              @click="emit('close')"
              class="text-gray-400 hover:text-gray-600 text-xl"
            >
              &times;
            </button>
          </div>
        </div>

        <!-- Content -->
        <div class="flex-1 overflow-y-auto p-4">
          <div v-if="loading" class="text-center text-gray-500 py-8">
            Loading...
          </div>
          <div v-else-if="files.length === 0" class="text-center text-gray-500 py-8">
            No pending files
          </div>
          <template v-else>
            <div v-for="(productFiles, productName) in groupedFiles" :key="productName" class="mb-6">
              <h3 class="font-medium text-gray-700 mb-2">{{ productName }}</h3>
              <div class="space-y-2">
                <div
                  v-for="file in productFiles"
                  :key="file.id"
                  class="flex items-center justify-between bg-gray-50 rounded p-3"
                >
                  <div class="flex-1 min-w-0 mr-3">
                    <div class="text-sm font-medium text-gray-900 truncate">{{ file.fileName }}</div>
                    <div class="text-xs text-gray-500">{{ formatFileSize(file.fileSize) }}</div>
                  </div>
                  <button
                    @click="downloadFile(file.id)"
                    :disabled="downloading.has(file.id)"
                    class="px-3 py-1 text-sm text-blue-600 hover:text-blue-800 disabled:opacity-50"
                  >
                    {{ downloading.has(file.id) ? 'Starting...' : 'Download' }}
                  </button>
                </div>
              </div>
            </div>
          </template>
        </div>
      </div>
    </div>
  </Teleport>
</template>
