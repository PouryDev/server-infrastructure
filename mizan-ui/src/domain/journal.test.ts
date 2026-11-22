import { describe, expect, it } from 'vitest'
import { buildDirectory } from './accounts'
import { addRowAfter, copyFromAbove, deleteRow, duplicateRow, emptyRow, swapSides, toApiLines, totals, validate, type DraftRow } from './journal'

const dir = buildDirectory(
  [
    { code: '11', level: 1, title: 'دارایی‌های جاری' },
    { code: '1110', parent: '11', level: 2, title: 'موجودی نقد و بانک' },
    { code: '111005', parent: '1110', level: 3, title: 'بانک', type_hint: 'asset' },
    { code: '111006', parent: '1110', level: 3, title: 'بانک پاسارگاد', type_hint: 'asset' },
  ],
  [{ code: 'bank.main', name: 'Bank', type: 'asset', coding_code: '111005', debit: 0, credit: 0, balance: 0 }],
)
const row = (p: Partial<DraftRow>): DraftRow => ({ ...emptyRow(), ...p })

describe('totals', () => {
  it('is balanced only when debit equals credit and is above zero', () => {
    expect(totals([row({ dr: '۵٬۸۰۰٬۰۰۰' }), row({ cr: '5800000' })])).toMatchObject({ debit: 5800000, credit: 5800000, balanced: true })
    expect(totals([row({ dr: '100' }), row({ cr: '90' })])).toMatchObject({ diff: 10, balanced: false })
    expect(totals([emptyRow()]).balanced).toBe(false)
  })
})

describe('validate', () => {
  it('resolves accounts, only subsidiary or mapped accounts are postable', () => {
    const v = validate('۱۴۰۵/۰۸/۱۹', [row({ code: '111005', dr: '100' }), row({ code: '111006', cr: '100' }), row({ code: '1110', cr: '1' }), row({ code: '999999', dr: '1' })], dir)
    expect(v.date).toBe('1405/08/19')
    expect(v.lines.map((l) => l.entry.code)).toEqual(['111005', '111006'])
    expect(v.issues.map((i) => i.row).sort()).toEqual([2, 3])
  })
  it('posts to the ledger account when mapped, to the new code otherwise', () => {
    const v = validate('1405/08/19', [row({ code: '111005', dr: '100' }), row({ code: '111006', cr: '100' })], dir)
    expect(toApiLines(v).map((l) => l.account)).toEqual(['bank.main', '111006'])
  })
  it('rejects both sides on one row and bad dates', () => {
    expect(validate('1405/13/40', [], dir).dateError).toBeTruthy()
    expect(validate('1405/08/19', [row({ code: '111005', dr: '1', cr: '1' })], dir).issues[0]?.field).toBe('amount')
  })
  it('ignores blank rows', () => {
    expect(validate('1405/08/19', [emptyRow(), emptyRow()], dir).issues).toEqual([])
  })
})

describe('keyboard row operations', () => {
  const rows = [row({ code: '111005', dr: '5' }), row({ code: '111006', cr: '5', desc: 'x' })]
  it('F3 swaps debit and credit', () => expect(swapSides(rows, 0)[0]).toMatchObject({ dr: '', cr: '5' }))
  it('F8 duplicates below', () => expect(duplicateRow(rows, 0)).toHaveLength(3))
  it('Ctrl+Insert adds a blank row after', () => expect(addRowAfter(rows, 0)[1]).toEqual(emptyRow()))
  it('Ctrl+Delete removes, never below one row', () => {
    expect(deleteRow(rows, 0)).toHaveLength(1)
    expect(deleteRow([rows[0]!], 0)).toEqual([emptyRow()])
  })
  it('F4 copies the focused field from the row above', () => {
    expect(copyFromAbove(rows, 1, 'code')[1]?.code).toBe('111005')
    const copied = copyFromAbove(rows, 1, 'dr')[1]!
    expect(copied).toMatchObject({ dr: '5', cr: '' })
    expect(copyFromAbove(rows, 0, 'code')).toBe(rows)
  })
})
