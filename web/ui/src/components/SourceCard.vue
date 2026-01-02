<script setup lang="ts">
import { ref } from 'vue'

interface Source {
  id: string
  name: string
  enabled: boolean
  hasCredentials: boolean
  credentialFields?: { key: string; label: string; type: string; required: boolean; helpText?: string }[]
}

const props = defineProps<{
  source: Source
  selected: boolean
  hasErrors?: boolean
}>()

const emit = defineEmits<{
  (e: 'select'): void
  (e: 'updated', enabled: boolean): void
}>()

const showConfig = ref(false)
const credentials = ref<Record<string, string>>({})
const testing = ref(false)
const saving = ref(false)
const testResult = ref<{ success: boolean; message: string } | null>(null)

async function testCredentials() {
  testing.value = true
  testResult.value = null

  try {
    const response = await fetch(`/api/sources/${props.source.id}/test`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ credentials: credentials.value }),
    })

    if (response.ok) {
      testResult.value = { success: true, message: 'Credentials valid!' }
    } else {
      const error = await response.json()
      testResult.value = { success: false, message: error.message || 'Invalid credentials' }
    }
  } catch {
    testResult.value = { success: false, message: 'Failed to test credentials' }
  }

  testing.value = false
}

async function saveConfig(enabled: boolean) {
  saving.value = true
  testResult.value = null

  try {
    const body: { enabled: boolean; credentials?: Record<string, string> } = { enabled }
    if (Object.keys(credentials.value).length > 0) {
      body.credentials = credentials.value
    }

    const response = await fetch(`/api/sources/${props.source.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(body),
    })

    if (response.ok) {
      showConfig.value = false
      emit('updated', enabled)
    } else {
      const error = await response.json()
      testResult.value = { success: false, message: error.message || 'Failed to save' }
    }
  } catch (error) {
    testResult.value = { success: false, message: 'Failed to save configuration' }
    console.error('Failed to save config:', error)
  }

  saving.value = false
}
</script>

<template>
  <div
    class="bg-white rounded-lg shadow p-4 cursor-pointer transition-all"
    :class="{ 'ring-2 ring-blue-500': selected }"
    @click="emit('select')"
  >
    <div class="flex items-center justify-between mb-2">
      <div class="flex items-center gap-2">
        <h3 class="font-medium text-gray-900">{{ source.name }}</h3>
        <span v-if="hasErrors" class="w-2 h-2 bg-red-500 rounded-full" title="Some files failed to download"></span>
      </div>
      <span
        class="px-2 py-1 text-xs rounded-full"
        :class="source.enabled ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-600'"
      >
        {{ source.enabled ? 'Enabled' : 'Disabled' }}
      </span>
    </div>

    <div class="text-sm text-gray-500 mb-3">
      {{ source.hasCredentials ? 'Credentials configured' : 'No credentials' }}
    </div>

    <button
      @click.stop="showConfig = true"
      class="text-sm text-blue-600 hover:text-blue-800"
    >
      Settings
    </button>

    <!-- Configuration Modal -->
    <Teleport to="body">
      <div
        v-if="showConfig"
        class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
        @click="showConfig = false"
      >
        <div
          class="bg-white rounded-lg shadow-xl p-6 w-full max-w-md"
          @click.stop
        >
          <h3 class="text-lg font-medium mb-4">Configure {{ source.name }}</h3>

          <form @submit.prevent="saveConfig(true)" class="space-y-4">
            <div v-for="field in source.credentialFields" :key="field.key">
              <label :for="field.key" class="block text-sm font-medium text-gray-700">
                {{ field.label }}
                <span v-if="field.required" class="text-red-500">*</span>
              </label>
              <input
                :id="field.key"
                v-model="credentials[field.key]"
                :type="field.type"
                class="mt-1 block w-full px-3 py-2 border border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
                :placeholder="field.helpText"
              />
            </div>

            <div v-if="testResult" class="text-sm" :class="testResult.success ? 'text-green-600' : 'text-red-600'">
              {{ testResult.message }}
            </div>

            <div class="flex justify-between">
              <button
                type="button"
                @click="testCredentials"
                :disabled="testing"
                class="px-4 py-2 text-sm text-blue-600 hover:text-blue-800 disabled:opacity-50"
              >
                {{ testing ? 'Testing...' : 'Test Credentials' }}
              </button>

              <div class="space-x-2">
                <button
                  v-if="source.enabled"
                  type="button"
                  @click="saveConfig(false)"
                  :disabled="saving"
                  class="px-4 py-2 text-sm text-gray-600 hover:text-gray-800"
                >
                  Disable
                </button>
                <button
                  type="submit"
                  :disabled="saving"
                  class="px-4 py-2 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
                >
                  {{ saving ? 'Saving...' : 'Save & Enable' }}
                </button>
              </div>
            </div>
          </form>

          <button
            @click="showConfig = false"
            class="absolute top-4 right-4 text-gray-400 hover:text-gray-600"
          >
            &times;
          </button>
        </div>
      </div>
    </Teleport>
  </div>
</template>
