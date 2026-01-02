import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useAuthStore = defineStore('auth', () => {
  const configured = ref(false)
  const authenticated = ref(false)

  async function checkStatus() {
    try {
      const response = await fetch('/api/auth/status', { credentials: 'include' })
      if (response.ok) {
        const data = await response.json()
        configured.value = data.configured
        authenticated.value = data.authenticated
      }
    } catch (error) {
      console.error('Failed to check auth status:', error)
    }
  }

  async function setup(passphrase: string): Promise<boolean> {
    try {
      const response = await fetch('/api/auth/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ passphrase }),
      })
      if (response.ok) {
        configured.value = true
        authenticated.value = true
        return true
      }
      return false
    } catch (error) {
      console.error('Setup failed:', error)
      return false
    }
  }

  async function login(passphrase: string): Promise<boolean> {
    try {
      const response = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ passphrase }),
      })
      if (response.ok) {
        authenticated.value = true
        return true
      }
      return false
    } catch (error) {
      console.error('Login failed:', error)
      return false
    }
  }

  async function logout() {
    try {
      await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' })
      authenticated.value = false
    } catch (error) {
      console.error('Logout failed:', error)
    }
  }

  return {
    configured,
    authenticated,
    checkStatus,
    setup,
    login,
    logout,
  }
})
