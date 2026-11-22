/** One key per user action: reuse it for every retry of that same action, make a new one for the next action. */
export function newIdempotencyKey(prefix = 'ui'): string {
  return `${prefix}-${crypto.randomUUID()}`
}

/**
 * Holds the key of the action in flight. `current()` returns the same key until `done()` is called (success or a final
 * answer), so a retry after a timeout cannot book twice.
 */
export function actionKey(prefix?: string) {
  let key: string | null = null
  return {
    current(): string {
      key ??= newIdempotencyKey(prefix)
      return key
    },
    done(): void {
      key = null
    },
  }
}
