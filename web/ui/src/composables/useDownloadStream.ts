import { ref, onMounted, onUnmounted } from 'vue'

export interface DownloadProgress {
  fileId: string
  fileName: string
  bytesWritten: number
  totalBytes: number
  startedAt: string
  speed: number
}

// Shared state across all consumers
const downloads = ref<DownloadProgress[]>([])
const statusVersion = ref(0)

let eventSource: EventSource | null = null
let refCount = 0
let reconnectTimer: ReturnType<typeof setTimeout> | null = null

function connect() {
  if (eventSource || refCount === 0) return

  eventSource = new EventSource('/api/downloads/active')

  eventSource.onmessage = (event) => {
    try {
      const payload = JSON.parse(event.data)
      downloads.value = payload.downloads || []
      statusVersion.value = payload.statusVersion || 0
    } catch {
      // Ignore parse errors
    }
  }

  eventSource.onerror = () => {
    disconnect()
    reconnectTimer = setTimeout(connect, 5000)
  }
}

function disconnect() {
  if (eventSource) {
    eventSource.close()
    eventSource = null
  }
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

export function useDownloadStream() {
  onMounted(() => {
    refCount++
    if (refCount === 1) {
      connect()
    }
  })

  onUnmounted(() => {
    refCount--
    if (refCount === 0) {
      disconnect()
    }
  })

  return { downloads, statusVersion }
}
