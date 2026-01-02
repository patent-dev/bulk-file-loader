<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useAuthStore } from './stores/auth'
import SetupWizard from './components/SetupWizard.vue'
import LoginForm from './components/LoginForm.vue'
import Dashboard from './components/Dashboard.vue'

const authStore = useAuthStore()
const loading = ref(true)

onMounted(async () => {
  await authStore.checkStatus()
  loading.value = false
})

const showSetup = computed(() => !authStore.configured)
const showLogin = computed(() => authStore.configured && !authStore.authenticated)
const showDashboard = computed(() => authStore.configured && authStore.authenticated)
</script>

<template>
  <div class="min-h-screen">
    <!-- Loading -->
    <div v-if="loading" class="flex items-center justify-center min-h-screen">
      <div class="text-gray-500">Loading...</div>
    </div>

    <!-- Setup Wizard (first run) -->
    <SetupWizard v-else-if="showSetup" />

    <!-- Login Form -->
    <LoginForm v-else-if="showLogin" />

    <!-- Main Dashboard -->
    <Dashboard v-else-if="showDashboard" />
  </div>
</template>
