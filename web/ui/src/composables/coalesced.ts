// coalesced wraps an async refresh so at most one run is in flight; calls that
// arrive meanwhile collapse into a single follow-up run.
export function coalesced(fn: () => Promise<void>): () => Promise<void> {
  let running: Promise<void> | null = null
  let again = false
  return () => {
    if (running) {
      again = true
      return running
    }
    running = (async () => {
      try {
        do {
          again = false
          await fn()
        } while (again)
      } finally {
        running = null
      }
    })()
    return running
  }
}
