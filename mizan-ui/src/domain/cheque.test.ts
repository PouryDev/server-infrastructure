import { describe, expect, it } from 'vitest'
import { allowedActions } from './cheque'

describe('cheque actions', () => {
  it('follows the treasury lifecycle', () => {
    expect(allowedActions({ direction: 'receivable', status: 'in_hand' })).toEqual(['deposit', 'return', 'endorse'])
    expect(allowedActions({ direction: 'receivable', status: 'deposited' })).toEqual(['clear', 'bounce'])
    expect(allowedActions({ direction: 'receivable', status: 'cleared' })).toEqual([])
    expect(allowedActions({ direction: 'payable', status: 'bounced' })).toEqual(['clear'])
  })
})
