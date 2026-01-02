<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useAuthStore } from '../stores/auth'
import Fuse from 'fuse.js'
import SourceCard from './SourceCard.vue'
import ActiveDownloads from './ActiveDownloads.vue'
import FileList from './FileList.vue'
import SettingsModal from './SettingsModal.vue'
import PendingFilesModal from './PendingFilesModal.vue'

interface Source {
  id: string
  name: string
  enabled: boolean
  hasCredentials: boolean
}

interface Product {
  id: string
  sourceId: string
  name: string
  description?: string
  autoDownload: boolean
  checkWindowStart?: string
  totalFiles?: number
  downloadedFiles?: number
  failedFiles?: number
}

interface Stats {
  totalFiles: number
  downloadedFiles: number
  pendingFiles: number
  activeDownloads: number
  enabledSources: number
}

const authStore = useAuthStore()

// Format cron schedule to human-readable string
function formatCronSchedule(cron: string): string {
  if (!cron) return ''

  const parts = cron.split(' ')
  if (parts.length !== 5) return cron

  const [minute, hour, dayOfMonth, month, dayOfWeek] = parts

  const dayNames: Record<string, string> = {
    '0': 'Sundays', '1': 'Mondays', '2': 'Tuesdays', '3': 'Wednesdays',
    '4': 'Thursdays', '5': 'Fridays', '6': 'Saturdays',
    'SUN': 'Sundays', 'MON': 'Mondays', 'TUE': 'Tuesdays', 'WED': 'Wednesdays',
    'THU': 'Thursdays', 'FRI': 'Fridays', 'SAT': 'Saturdays',
  }

  const formatTime = (h: string, m: string): string => {
    const hour24 = parseInt(h, 10)
    const min = m.padStart(2, '0')
    if (hour24 === 0) return `12:${min} AM`
    if (hour24 < 12) return `${hour24}:${min} AM`
    if (hour24 === 12) return `12:${min} PM`
    return `${hour24 - 12}:${min} PM`
  }

  // Every N hours: 0 */6 * * *
  if (hour.startsWith('*/')) {
    const interval = hour.slice(2)
    return `Every ${interval} hours`
  }

  // Specific day of week: 0 6 * * TUE
  if (dayOfWeek !== '*') {
    const day = dayNames[dayOfWeek.toUpperCase()] || dayOfWeek
    return `${day} at ${formatTime(hour, minute)}`
  }

  // Daily: 0 6 * * *
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '*') {
    return `Daily at ${formatTime(hour, minute)}`
  }

  // Monthly: 0 6 1 * *
  if (dayOfMonth !== '*' && month === '*') {
    return `Monthly on day ${dayOfMonth} at ${formatTime(hour, minute)}`
  }

  return cron
}
const sources = ref<Source[]>([])
const products = ref<Product[]>([])
const sourceErrors = ref<Set<string>>(new Set()) // Track which sources have failed files
const stats = ref<Stats | null>(null)
const showSettings = ref(false)
const showPendingFiles = ref(false)
const selectedSourceId = ref<string | null>(localStorage.getItem('selectedSourceId'))
const selectedProductId = ref<string | null>(localStorage.getItem('selectedProductId'))
const loadingProducts = ref(false)
const refreshInterval = ref<number | null>(null)
const productRefreshInterval = ref<number | null>(null)
const productSearch = ref('')
const productFilter = ref<'all' | 'enabled'>('all')

// Check if selected source is enabled
const selectedSourceEnabled = computed(() => {
  if (!selectedSourceId.value) return false
  const source = sources.value.find(s => s.id === selectedSourceId.value)
  return source?.enabled ?? false
})

// Filtered and searched products
const filteredProducts = computed(() => {
  let result = products.value

  // Apply auto filter
  if (productFilter.value === 'enabled') {
    result = result.filter(p => p.autoDownload)
  }

  // Apply search
  if (productSearch.value.trim()) {
    const fuse = new Fuse(result, {
      keys: ['name', 'description'],
      threshold: 0.4,
      ignoreLocation: true,
    })
    result = fuse.search(productSearch.value).map(r => r.item)
  }

  return result
})

// Set default filter when products are loaded
watch(products, (newProducts) => {
  if (newProducts.length > 0 && newProducts.some(p => p.autoDownload)) {
    productFilter.value = 'enabled'
  } else {
    productFilter.value = 'all'
  }
}, { immediate: true })

// Persist navigation state
watch(selectedSourceId, (id) => {
  if (id) {
    localStorage.setItem('selectedSourceId', id)
  } else {
    localStorage.removeItem('selectedSourceId')
  }
})

watch(selectedProductId, (id) => {
  if (id) {
    localStorage.setItem('selectedProductId', id)
  } else {
    localStorage.removeItem('selectedProductId')
  }
})

async function fetchSources() {
  try {
    const response = await fetch('/api/sources', { credentials: 'include' })
    if (response.ok) {
      sources.value = await response.json()
    }
  } catch (error) {
    console.error('Failed to fetch sources:', error)
  }
}

async function fetchProducts() {
  if (!selectedSourceId.value) {
    products.value = []
    stopProductRefresh()
    return
  }

  // Only show loading on initial load
  if (products.value.length === 0) {
    loadingProducts.value = true
  }
  try {
    const response = await fetch(`/api/products?sourceId=${selectedSourceId.value}`, {
      credentials: 'include',
    })
    if (response.ok) {
      const newProducts: Product[] = await response.json()
      // Update in-place if structure is same, otherwise replace
      if (products.value.length === newProducts.length &&
          products.value.every((p, i) => p.id === newProducts[i]?.id)) {
        // Same structure - update in-place
        const newMap = new Map(newProducts.map(p => [p.id, p]))
        for (const product of products.value) {
          const updated = newMap.get(product.id)
          if (updated) {
            Object.assign(product, updated)
          }
        }
      } else {
        // Structure changed - replace array
        products.value = newProducts
      }
      // Start/adjust product refresh based on whether we have products
      startProductRefresh()
    }
  } catch (error) {
    console.error('Failed to fetch products:', error)
  }
  loadingProducts.value = false
}

function startProductRefresh() {
  if (productRefreshInterval.value) {
    clearInterval(productRefreshInterval.value)
  }
  if (!selectedSourceId.value) return
  // 5s when no products, 30s when products exist
  const interval = products.value.length === 0 ? 5000 : 30000
  productRefreshInterval.value = window.setInterval(() => {
    fetchProducts()
  }, interval)
}

function stopProductRefresh() {
  if (productRefreshInterval.value) {
    clearInterval(productRefreshInterval.value)
    productRefreshInterval.value = null
  }
}

async function fetchStats() {
  try {
    const response = await fetch('/api/stats', { credentials: 'include' })
    if (response.ok) {
      stats.value = await response.json()
    }
  } catch (error) {
    console.error('Failed to fetch stats:', error)
  }
}

// Update product counts in-place without replacing the array (avoids flicker)
async function refreshProductCounts() {
  if (!selectedSourceId.value || products.value.length === 0) return

  try {
    const response = await fetch(`/api/products?sourceId=${selectedSourceId.value}`, {
      credentials: 'include',
    })
    if (response.ok) {
      const newProducts: Product[] = await response.json()
      const newMap = new Map(newProducts.map(p => [p.id, p]))

      // Update counts in-place
      for (const product of products.value) {
        const updated = newMap.get(product.id)
        if (updated) {
          if (product.totalFiles !== updated.totalFiles) product.totalFiles = updated.totalFiles
          if (product.downloadedFiles !== updated.downloadedFiles) product.downloadedFiles = updated.downloadedFiles
          if (product.failedFiles !== updated.failedFiles) product.failedFiles = updated.failedFiles
        }
      }
    }
  } catch (error) {
    console.error('Failed to refresh product counts:', error)
  }
}

// Called when files change (download, skip, delete, etc.)
async function handleFilesChanged() {
  await Promise.all([fetchStats(), fetchSourceErrors(), refreshProductCounts()])
}

async function fetchSourceErrors() {
  try {
    // Fetch all products to check which sources have failed files
    const response = await fetch('/api/products', { credentials: 'include' })
    if (response.ok) {
      const allProducts: Product[] = await response.json()
      const errors = new Set<string>()
      for (const p of allProducts) {
        if (p.failedFiles && p.failedFiles > 0) {
          errors.add(p.sourceId)
        }
      }
      sourceErrors.value = errors
    }
  } catch (error) {
    console.error('Failed to fetch source errors:', error)
  }
}

async function handleSourceUpdated(sourceId: string, enabled: boolean) {
  await fetchSources()
  fetchStats()
  fetchSourceErrors()
  // Select and fetch products if source was enabled
  if (enabled) {
    selectedSourceId.value = sourceId
    selectedProductId.value = null
    await fetchProducts()
  } else if (selectedSourceId.value) {
    fetchProducts()
  }
}

function selectSource(sourceId: string) {
  if (selectedSourceId.value === sourceId) {
    selectedSourceId.value = null
    selectedProductId.value = null
  } else {
    selectedSourceId.value = sourceId
    selectedProductId.value = null
  }
}

function selectProduct(productId: string) {
  selectedProductId.value = selectedProductId.value === productId ? null : productId
}

async function toggleAutoDownload(product: Product, event: Event) {
  event.stopPropagation()
  try {
    const response = await fetch(`/api/schedule/${encodeURIComponent(product.id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ autoDownload: !product.autoDownload }),
    })
    if (response.ok) {
      // Refresh products and stats to get updated state from server
      await fetchProducts()
      fetchStats()
    }
  } catch (error) {
    console.error('Failed to toggle auto-download:', error)
  }
}

async function updateSchedule(productId: string, schedule: string) {
  try {
    const response = await fetch(`/api/schedule/${encodeURIComponent(productId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ checkWindowStart: schedule }),
    })
    if (response.ok) {
      await fetchProducts()
    }
  } catch (error) {
    console.error('Failed to update schedule:', error)
  }
}

async function syncSource(sourceId: string) {
  try {
    // Re-enable to trigger sync
    await fetch(`/api/sources/${sourceId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ enabled: true }),
    })
    // Wait a bit and refresh products
    setTimeout(() => {
      fetchProducts()
    }, 2000)
  } catch (error) {
    console.error('Failed to sync source:', error)
  }
}

// Fetch products when source changes (but not during initial restoration)
let isInitialLoad = true
watch(selectedSourceId, () => {
  if (isInitialLoad) {
    return
  }
  selectedProductId.value = null
  fetchProducts()
})

onMounted(async () => {
  await fetchSources()
  fetchStats()
  fetchSourceErrors()

  // Restore products if source was persisted
  if (selectedSourceId.value) {
    await fetchProducts()
  }

  // Allow watcher to handle subsequent source changes
  isInitialLoad = false

  refreshInterval.value = window.setInterval(() => {
    fetchStats()
    fetchSourceErrors()
    refreshProductCounts()
  }, 10000)
})

onUnmounted(() => {
  if (refreshInterval.value) {
    clearInterval(refreshInterval.value)
  }
  stopProductRefresh()
})
</script>

<template>
  <div class="min-h-screen bg-gray-50 flex flex-col">
    <!-- Header -->
    <header class="bg-white shadow-sm">
      <div class="max-w-7xl mx-auto px-4 py-4 sm:px-6 lg:px-8 flex justify-between items-center">
        <div class="flex items-center space-x-3">
          <img src="/logo.svg" alt="patent.dev" class="h-7" />
          <div>
            <h1 class="text-xl font-semibold text-gray-900">Bulk File Loader</h1>
            <p class="text-xs text-gray-400">by patent.dev</p>
          </div>
        </div>
        <div class="flex items-center space-x-4">
          <button
            @click="showSettings = true"
            class="text-gray-500 hover:text-gray-700"
          >
            Settings
          </button>
          <button
            @click="authStore.logout()"
            class="text-gray-500 hover:text-gray-700"
          >
            Logout
          </button>
        </div>
      </div>
    </header>

    <main class="w-full max-w-7xl mx-auto px-4 py-6 sm:px-6 lg:px-8">
      <!-- Stats Overview -->
      <div v-if="stats" class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <div class="bg-white rounded-lg shadow p-4">
          <div class="text-sm text-gray-500">Total Files</div>
          <div class="text-2xl font-bold">{{ stats.totalFiles }}</div>
        </div>
        <div class="bg-white rounded-lg shadow p-4">
          <div class="text-sm text-gray-500">Downloaded</div>
          <div class="text-2xl font-bold text-green-600">{{ stats.downloadedFiles }}</div>
        </div>
        <div
          class="bg-white rounded-lg shadow p-4 cursor-pointer hover:shadow-md transition-shadow"
          @click="showPendingFiles = true"
        >
          <div class="text-sm text-gray-500">Pending</div>
          <div class="text-2xl font-bold text-yellow-600">{{ stats.pendingFiles }}</div>
        </div>
        <div class="bg-white rounded-lg shadow p-4">
          <div class="text-sm text-gray-500">Active Downloads</div>
          <div class="text-2xl font-bold text-blue-600">{{ stats.activeDownloads }}</div>
        </div>
      </div>

      <!-- Active Downloads -->
      <section class="mb-6">
        <ActiveDownloads />
      </section>

      <!-- Step 1: Sources -->
      <section class="mb-6">
        <h2 class="text-lg font-medium text-gray-900 mb-3">
          <span class="inline-flex items-center justify-center w-6 h-6 rounded-full bg-blue-600 text-white text-sm mr-2">1</span>
          Select Source
        </h2>
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <SourceCard
            v-for="source in sources"
            :key="source.id"
            :source="source"
            :selected="selectedSourceId === source.id"
            :has-errors="sourceErrors.has(source.id)"
            @select="selectSource(source.id)"
            @updated="(enabled: boolean) => handleSourceUpdated(source.id, enabled)"
          />
          <!-- Request New Source Card -->
          <div class="bg-white rounded-lg border-2 border-dashed border-gray-300 p-4 flex flex-col justify-between">
            <div>
              <h3 class="font-medium text-gray-600">Request New Source</h3>
              <p class="text-sm text-gray-400 mt-2">Need data from another patent office?</p>
            </div>
            <a
              href="mailto:info@patent.dev"
              class="mt-3 text-sm text-blue-600 hover:text-blue-800"
            >
              Contact Wolfgang Stark
            </a>
          </div>
        </div>
      </section>

      <!-- Step 2: Products (shown when source is selected and enabled) -->
      <section v-if="selectedSourceId && selectedSourceEnabled" class="mb-6">
        <div class="flex items-center justify-between mb-3">
          <h2 class="text-lg font-medium text-gray-900">
            <span class="inline-flex items-center justify-center w-6 h-6 rounded-full bg-blue-600 text-white text-sm mr-2">2</span>
            {{ selectedProductId ? 'Product' : 'Select Product' }}
          </h2>
          <button
            v-if="!selectedProductId"
            @click="syncSource(selectedSourceId!)"
            class="text-sm text-blue-600 hover:text-blue-800 flex items-center"
            title="Refresh products from source"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
          </button>
        </div>
        <button
          v-if="selectedProductId"
          @click="selectedProductId = null"
          class="mb-3 text-sm text-blue-600 hover:text-blue-800 flex items-center"
        >
          <svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
          </svg>
          Back to products
        </button>
        <div v-if="loadingProducts" class="text-gray-500">Loading products...</div>
        <div v-else-if="products.length === 0" class="bg-white rounded-lg shadow p-4 text-gray-500">
          <div class="flex items-center justify-center gap-2">
            <svg class="animate-spin h-4 w-4 text-blue-500" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
            </svg>
            Syncing products...
          </div>
        </div>
        <!-- Show only selected product when one is selected -->
        <div v-else-if="selectedProductId" class="grid grid-cols-1 gap-3">
          <div
            v-for="product in products.filter(p => p.id === selectedProductId)"
            :key="product.id"
            class="bg-white rounded-lg shadow p-4 ring-2 ring-blue-500 bg-blue-50"
          >
            <div class="flex items-start justify-between">
              <div class="font-medium text-gray-900 flex-1">{{ product.name }}</div>
              <button
                @click="toggleAutoDownload(product, $event)"
                class="ml-2 flex items-center gap-1.5 px-2 py-1 text-xs rounded border transition-colors cursor-pointer"
                :class="product.autoDownload
                  ? 'bg-green-50 text-green-700 border-green-200 hover:bg-green-100'
                  : 'bg-gray-50 text-gray-500 border-gray-200 hover:bg-gray-100'"
                :title="product.autoDownload ? 'Click to disable auto-download' : 'Click to enable auto-download'"
              >
                <span
                  class="w-6 h-3 rounded-full relative transition-colors"
                  :class="product.autoDownload ? 'bg-green-500' : 'bg-gray-300'"
                >
                  <span
                    class="absolute top-0.5 w-2 h-2 rounded-full bg-white shadow transition-transform"
                    :class="product.autoDownload ? 'right-0.5' : 'left-0.5'"
                  ></span>
                </span>
                {{ product.autoDownload ? 'Enabled' : 'Manual' }}
              </button>
            </div>
            <div v-if="product.description" class="text-sm text-gray-500 mt-1">
              {{ product.description }}
            </div>
            <div v-if="product.autoDownload" class="flex items-center gap-2 mt-2">
              <label class="text-xs text-gray-500">Check schedule:</label>
              <select
                @change="updateSchedule(product.id, ($event.target as HTMLSelectElement).value)"
                @click.stop
                class="text-xs border border-gray-300 rounded px-2 py-1"
              >
                <option value="0 6 * * *" :selected="product.checkWindowStart === '0 6 * * *'">Daily (6 AM)</option>
                <option value="0 6 * * TUE" :selected="product.checkWindowStart === '0 6 * * TUE'">Tuesdays (6 AM)</option>
                <option value="0 6 * * 1" :selected="product.checkWindowStart === '0 6 * * 1'">Mondays (6 AM)</option>
                <option value="0 6 1 * *" :selected="product.checkWindowStart === '0 6 1 * *'">Monthly (1st at 6 AM)</option>
                <option value="0 6 1 1 *" :selected="product.checkWindowStart === '0 6 1 1 *'">Yearly (Jan 1 at 6 AM)</option>
              </select>
            </div>
            <div v-if="product.totalFiles" class="flex items-center gap-2 mt-2 text-xs">
              <span class="text-gray-500">{{ product.downloadedFiles || 0 }}/{{ product.totalFiles }} downloaded</span>
              <span v-if="product.failedFiles" class="text-red-600">{{ product.failedFiles }} failed</span>
            </div>
          </div>
        </div>
        <!-- Show all products when none is selected -->
        <template v-else>
          <!-- Search and filter bar -->
          <div class="mb-3 flex gap-3">
            <input
              v-model="productSearch"
              type="text"
              placeholder="Search products..."
              class="flex-1 px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
            <div class="flex items-center gap-2">
              <svg class="w-4 h-4 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" />
              </svg>
              <div class="flex rounded-md overflow-hidden border border-gray-300">
                <button
                  @click="productFilter = 'enabled'"
                  class="px-3 py-2 text-sm transition-colors"
                  :class="productFilter === 'enabled' ? 'bg-blue-600 text-white' : 'bg-white text-gray-600 hover:bg-gray-50'"
                >
                  Enabled
                </button>
                <button
                  @click="productFilter = 'all'"
                  class="px-3 py-2 text-sm border-l border-gray-300 transition-colors"
                  :class="productFilter === 'all' ? 'bg-blue-600 text-white' : 'bg-white text-gray-600 hover:bg-gray-50'"
                >
                  All
                </button>
              </div>
            </div>
          </div>
          <div v-if="filteredProducts.length === 0" class="bg-white rounded-lg shadow p-4 text-gray-500">
            No products match {{ productSearch ? `"${productSearch}"` : 'the current filter' }}
          </div>
          <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            <div
              v-for="product in filteredProducts"
              :key="product.id"
              @click="selectProduct(product.id); productSearch = ''"
              class="bg-white rounded-lg shadow p-4 cursor-pointer transition-all hover:shadow-md"
            >
              <div class="flex items-start justify-between">
                <div class="font-medium text-gray-900 flex-1">{{ product.name }}</div>
                <button
                  @click="toggleAutoDownload(product, $event)"
                  class="ml-2 flex items-center gap-1.5 px-2 py-1 text-xs rounded border transition-colors cursor-pointer"
                  :class="product.autoDownload
                    ? 'bg-green-50 text-green-700 border-green-200 hover:bg-green-100'
                    : 'bg-gray-50 text-gray-500 border-gray-200 hover:bg-gray-100'"
                  :title="product.autoDownload ? 'Click to disable auto-download' : 'Click to enable auto-download'"
                >
                  <span
                    class="w-6 h-3 rounded-full relative transition-colors"
                    :class="product.autoDownload ? 'bg-green-500' : 'bg-gray-300'"
                  >
                    <span
                      class="absolute top-0.5 w-2 h-2 rounded-full bg-white shadow transition-transform"
                      :class="product.autoDownload ? 'right-0.5' : 'left-0.5'"
                    ></span>
                  </span>
                  {{ product.autoDownload ? 'Enabled' : 'Manual' }}
              </button>
            </div>
            <div v-if="product.description" class="text-sm text-gray-500 mt-1 line-clamp-2">
              {{ product.description }}
            </div>
            <div v-if="product.autoDownload && product.checkWindowStart" class="text-xs text-blue-600 mt-1">
              Checks: {{ formatCronSchedule(product.checkWindowStart) }}
            </div>
            <div v-if="product.totalFiles" class="flex items-center gap-2 mt-2 text-xs">
              <span class="text-gray-500">{{ product.downloadedFiles || 0 }}/{{ product.totalFiles }}</span>
              <span v-if="product.failedFiles" class="px-1.5 py-0.5 bg-red-100 text-red-600 rounded">{{ product.failedFiles }} failed</span>
            </div>
          </div>
        </div>
        </template>
      </section>

      <!-- Step 3: Files (shown when product is selected) -->
      <section v-if="selectedProductId">
        <h2 class="text-lg font-medium text-gray-900 mb-3">
          <span class="inline-flex items-center justify-center w-6 h-6 rounded-full bg-blue-600 text-white text-sm mr-2">3</span>
          Files
        </h2>
        <FileList :product-id="selectedProductId" @files-changed="handleFilesChanged" />
      </section>

      <!-- Placeholder when no product selected -->
      <section v-else-if="selectedSourceId && selectedSourceEnabled && products.length > 0" class="bg-white rounded-lg shadow p-8 text-center text-gray-500">
        Select a product above to see available files
      </section>
    </main>

    <!-- Settings Modal -->
    <SettingsModal v-if="showSettings" @close="showSettings = false" />

    <!-- Pending Files Modal -->
    <PendingFilesModal
      v-if="showPendingFiles"
      @close="showPendingFiles = false"
      @files-changed="handleFilesChanged"
    />

    <!-- Footer -->
    <footer class="mt-auto py-6 text-center text-sm text-gray-400">
      Built by <a href="https://patent.dev/" target="_blank" rel="noopener" class="text-blue-500 hover:text-blue-600">patent.dev</a>
    </footer>
  </div>
</template>
