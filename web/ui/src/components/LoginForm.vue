<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '../stores/auth'

const authStore = useAuthStore()
const passphrase = ref('')
const error = ref('')
const loading = ref(false)

async function handleLogin() {
  error.value = ''
  loading.value = true

  const success = await authStore.login(passphrase.value)
  loading.value = false

  if (!success) {
    error.value = 'Invalid passphrase'
    passphrase.value = ''
  }
}
</script>

<template>
  <div class="flex items-center justify-center min-h-screen bg-gray-100">
    <div class="w-full max-w-md p-8 bg-white rounded-lg shadow-md">
      <h1 class="text-2xl font-bold text-center text-gray-800 mb-1">
        Bulk File Loader
      </h1>
      <p class="text-center text-sm text-gray-400 mb-6">by patent.dev</p>

      <form @submit.prevent="handleLogin" class="space-y-4">
        <div>
          <label for="passphrase" class="block text-sm font-medium text-gray-700">
            Passphrase
          </label>
          <input
            id="passphrase"
            v-model="passphrase"
            type="password"
            class="mt-1 block w-full px-3 py-2 border border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
            placeholder="Enter your passphrase"
            required
            autofocus
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
          {{ loading ? 'Logging in...' : 'Login' }}
        </button>
      </form>
    </div>
  </div>
</template>
