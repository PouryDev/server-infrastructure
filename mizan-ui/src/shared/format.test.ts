import { describe, expect, it } from 'vitest'
import { fmt, parseAmount, toFa, toLatin } from './digits'
import { normalizeJalali } from './jalali'

describe('digits', () => {
  it('formats rials with Persian digits and ٬', () => {
    expect(fmt(5800000)).toBe('۵٬۸۰۰٬۰۰۰')
    expect(fmt(-18050000000)).toBe('−۱۸٬۰۵۰٬۰۰۰٬۰۰۰')
    expect(fmt(0)).toBe('۰')
  })
  it('parses what accountants type', () => {
    expect(parseAmount('۵٬۸۰۰٬۰۰۰')).toBe(5800000)
    expect(parseAmount('5,800,000')).toBe(5800000)
    expect(parseAmount('')).toBe(0)
    expect(Number.isNaN(parseAmount('12.5'))).toBe(true)
    expect(Number.isNaN(parseAmount('abc'))).toBe(true)
  })
  it('converts digits both ways', () => {
    expect(toLatin('۱۴۰۵/۰۸/۱۹')).toBe('1405/08/19')
    expect(toFa('1405')).toBe('۱۴۰۵')
  })
})

describe('jalali', () => {
  it('normalises input dates', () => {
    expect(normalizeJalali('۱۴۰۵-۸-۹')).toBe('1405/08/09')
    expect(normalizeJalali('1405/07/31')).toBeNull()
    expect(normalizeJalali('hello')).toBeNull()
  })
})

import { isoToJalali, jalaliToIso } from './jalali'
describe('jalali conversion', () => {
  it('converts both ways for known dates', () => {
    expect(jalaliToIso('1405/08/19')).toBe('2026-11-10')
    expect(isoToJalali('2026-11-10')).toBe('1405/08/19')
    expect(isoToJalali('2026-03-21T00:00:00Z')).toBe('1405/01/01')
    expect(jalaliToIso('1405/12/29')).toBe('2027-03-20')
    expect(isoToJalali('2027-03-20')).toBe('1405/12/29')
    expect(jalaliToIso('1403/12/30')).toBe('2025-03-20') // 1403 is a leap year
    expect(isoToJalali('2025-03-20')).toBe('1403/12/30')
  })
  it('round-trips a whole span', () => {
    for (let d = new Date('2020-01-01T00:00:00Z'); d < new Date('2032-01-01T00:00:00Z'); d = new Date(d.getTime() + 86400000 * 7)) {
      const iso = d.toISOString().slice(0, 10)
      expect(jalaliToIso(isoToJalali(iso))).toBe(iso)
    }
  })
})
