<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '../stores/auth'

const authStore = useAuthStore()
const passphrase = ref('')
const confirmPassphrase = ref('')
const error = ref('')
const loading = ref(false)

async function handleSetup() {
  error.value = ''

  if (passphrase.value.length < 8) {
    error.value = 'Passphrase must be at least 8 characters'
    return
  }

  if (passphrase.value !== confirmPassphrase.value) {
    error.value = 'Passphrases do not match'
    return
  }

  loading.value = true
  const success = await authStore.setup(passphrase.value)
  loading.value = false

  if (!success) {
    error.value = 'Setup failed. Please try again.'
  }
}
</script>

<template>
  <div class="flex items-center justify-center min-h-screen bg-gray-100">
    <div class="w-full max-w-md p-8 bg-white rounded-lg shadow-md">
      <h1 class="text-2xl font-bold text-center text-gray-800 mb-1">
        Bulk File Loader
      </h1>
      <p class="text-center text-sm text-gray-400 mb-4">by patent.dev</p>
      <p class="text-center text-gray-600 mb-6">
        Set up your passphrase to get started
      </p>

      <form @submit.prevent="handleSetup" class="space-y-4">
        <div>
          <label for="passphrase" class="block text-sm font-medium text-gray-700">
            Passphrase
          </label>
          <input
            id="passphrase"
            v-model="passphrase"
            type="password"
            class="mt-1 block w-full px-3 py-2 border border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
            placeholder="Enter passphrase (min 8 characters)"
            required
          />
        </div>

        <div>
          <label for="confirm" class="block text-sm font-medium text-gray-700">
            Confirm Passphrase
          </label>
          <input
            id="confirm"
            v-model="confirmPassphrase"
            type="password"
            class="mt-1 block w-full px-3 py-2 border border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
            placeholder="Confirm passphrase"
            required
          />
        </div>

        <div v-if="error" class="text-red-600 text-sm">
          {{ error }}
        </div>

        <button
          type="submit"
          :disabled="loading"
          class="w-full py-2 px-4 border border-transparent rounded-md shadow-sm text-sm font-medium text-white bg-blue-600 hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500 disabled:opacity-50"
        >
          {{ loading ? 'Setting up...' : 'Set Passphrase' }}
        </button>
      </form>

      <p class="mt-4 text-xs text-gray-500 text-center">
        This passphrase will be used to encrypt your API credentials and authenticate access.
      </p>
    </div>
  </div>
</template>
