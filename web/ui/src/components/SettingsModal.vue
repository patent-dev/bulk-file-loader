<script setup lang="ts">
import { ref, onMounted } from 'vue'

interface Webhook {
  id: number
  name: string
  url: string
  events: string[]
  enabled: boolean
}

const emit = defineEmits<{
  (e: 'close'): void
}>()

const webhooks = ref<Webhook[]>([])
const newWebhook = ref({ name: '', url: '', events: ['download.completed', 'download.failed'] })
const showAddWebhook = ref(false)
const saving = ref(false)

const availableEvents = [
  'file.available',
  'download.started',
  'download.completed',
  'download.failed',
  'download.cancelled',
  'checksum.mismatch',
  'sync.completed',
  'sync.failed',
]

async function fetchWebhooks() {
  try {
    const response = await fetch('/api/hooks', { credentials: 'include' })
    if (response.ok) {
      webhooks.value = await response.json()
    }
  } catch (error) {
    console.error('Failed to fetch webhooks:', error)
  }
}

async function createWebhook() {
  saving.value = true

  try {
    const response = await fetch('/api/hooks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(newWebhook.value),
    })

    if (response.ok) {
      showAddWebhook.value = false
      newWebhook.value = { name: '', url: '', events: ['download.completed', 'download.failed'] }
      fetchWebhooks()
    }
  } catch (error) {
    console.error('Failed to create webhook:', error)
  }

  saving.value = false
}

async function deleteWebhook(id: number) {
  try {
    await fetch(`/api/hooks/${id}`, { method: 'DELETE', credentials: 'include' })
    fetchWebhooks()
  } catch (error) {
    console.error('Failed to delete webhook:', error)
  }
}

async function toggleWebhook(webhook: Webhook) {
  try {
    await fetch(`/api/hooks/${webhook.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ ...webhook, enabled: !webhook.enabled }),
    })
    fetchWebhooks()
  } catch (error) {
    console.error('Failed to toggle webhook:', error)
  }
}

onMounted(() => {
  fetchWebhooks()
})
</script>

<template>
  <div
    class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
    @click="emit('close')"
  >
    <div
      class="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[80vh] overflow-hidden"
      @click.stop
    >
      <div class="p-4 border-b flex justify-between items-center">
        <h2 class="text-lg font-medium">Settings</h2>
        <button @click="emit('close')" class="text-gray-400 hover:text-gray-600 text-xl">
          &times;
        </button>
      </div>

      <div class="p-4 overflow-y-auto max-h-[60vh]">
        <!-- Webhooks Section -->
        <section>
          <div class="flex justify-between items-center mb-4">
            <h3 class="font-medium">Webhooks</h3>
            <button
              @click="showAddWebhook = true"
              class="text-sm text-blue-600 hover:text-blue-800"
            >
              Add Webhook
            </button>
          </div>

          <div v-if="webhooks.length === 0" class="text-gray-500 text-sm">
            No webhooks configured
          </div>

          <div v-else class="space-y-3">
            <div
              v-for="webhook in webhooks"
              :key="webhook.id"
              class="border rounded-lg p-3"
            >
              <div class="flex justify-between items-start mb-2">
                <div>
                  <div class="font-medium">{{ webhook.name }}</div>
                  <div class="text-sm text-gray-500 truncate">{{ webhook.url }}</div>
                </div>
                <div class="flex items-center space-x-2">
                  <button
                    @click="toggleWebhook(webhook)"
                    class="text-sm"
                    :class="webhook.enabled ? 'text-green-600' : 'text-gray-400'"
                  >
                    {{ webhook.enabled ? 'Enabled' : 'Disabled' }}
                  </button>
                  <button
                    @click="deleteWebhook(webhook.id)"
                    class="text-sm text-red-600 hover:text-red-800"
                  >
                    Delete
                  </button>
                </div>
              </div>
              <div class="flex flex-wrap gap-1">
                <span
                  v-for="event in webhook.events"
                  :key="event"
                  class="px-2 py-0.5 text-xs bg-gray-100 rounded"
                >
                  {{ event }}
                </span>
              </div>
            </div>
          </div>
        </section>

        <!-- Add Webhook Form -->
        <div v-if="showAddWebhook" class="mt-4 border rounded-lg p-4 bg-gray-50">
          <h4 class="font-medium mb-3">New Webhook</h4>
          <form @submit.prevent="createWebhook" class="space-y-3">
            <div>
              <label class="block text-sm font-medium text-gray-700">Name</label>
              <input
                v-model="newWebhook.name"
                type="text"
                class="mt-1 block w-full px-3 py-2 border border-gray-300 rounded-md"
                required
              />
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">URL</label>
              <input
                v-model="newWebhook.url"
                type="url"
                class="mt-1 block w-full px-3 py-2 border border-gray-300 rounded-md"
                placeholder="https://example.com/webhook"
                required
              />
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700 mb-2">Events</label>
              <div class="grid grid-cols-2 gap-2">
                <label
                  v-for="event in availableEvents"
                  :key="event"
                  class="flex items-center text-sm"
                >
                  <input
                    type="checkbox"
                    :value="event"
                    v-model="newWebhook.events"
                    class="mr-2"
                  />
                  {{ event }}
                </label>
              </div>
            </div>
            <div class="flex justify-end space-x-2">
              <button
                type="button"
                @click="showAddWebhook = false"
                class="px-4 py-2 text-sm text-gray-600 hover:text-gray-800"
              >
                Cancel
              </button>
              <button
                type="submit"
                :disabled="saving"
                class="px-4 py-2 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
              >
                {{ saving ? 'Creating...' : 'Create' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  </div>
</template>
