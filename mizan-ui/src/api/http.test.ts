import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { friendly, ApiError } from './errors'
import { api, money, onSessionExpired, toSearch } from './http'
import { listQuery } from './page'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

describe('query building', () => {
  it('serialises gokit list syntax', () => {
    const q = listQuery({ page: 2, pageSize: 20, sort: '-date', filter: { status: 'issued', 'date:gte': '2026-03-21', name: ['like', 'نیلو'] } })
    expect(q).toMatchObject({ page: 2, page_size: 20, sort: '-date', 'filter[status]': 'issued', 'filter[date][gte]': '2026-03-21', 'filter[name][like]': 'نیلو' })
    expect(toSearch({ a: 1, b: undefined, c: '', d: ['x', 'y'] })).toBe('?a=1&d=x%2Cy')
  })
})

describe('http client', () => {
  const fetchMock = vi.fn<typeof fetch>()
  beforeEach(() => vi.stubGlobal('fetch', fetchMock))
  afterEach(() => (fetchMock.mockReset(), vi.unstubAllGlobals()))

  it('turns Money objects into whole rials and sends the CSRF + idempotency headers on unsafe calls', async () => {
    fetchMock.mockResolvedValueOnce(json(201, { id: 1, total: { amount: 5800000, currency: 'IRR' }, lines: [{ amount: { amount: 7, currency: 'IRR' }, memo: 'x' }] }))
    const r = await api.post<{ total: number; lines: { amount: number }[] }>('/v1/accounting/journals', { amount: money(5) }, 'key-1')
    expect(r.total).toBe(5800000)
    expect(r.lines[0]!.amount).toBe(7)
    const [, init] = fetchMock.mock.calls[0]!
    const h = init!.headers as Record<string, string>
    expect(h['Idempotency-Key']).toBe('key-1')
    expect(h['X-Mizan-UI']).toBe('1')
    expect(JSON.parse(init!.body as string)).toEqual({ amount: { amount: 5, currency: 'IRR' } })
  })

  it('does not send the CSRF header on reads', async () => {
    fetchMock.mockResolvedValueOnce(json(200, []))
    await api.get('/v1/accounting/fiscal-years')
    expect((fetchMock.mock.calls[0]![1]!.headers as Record<string, string>)['X-Mizan-UI']).toBeUndefined()
  })

  it('maps the gokit error shape to ApiError and a friendly Persian message', async () => {
    fetchMock.mockResolvedValueOnce(json(409, { error: { code: 'period_closed', message: 'the accounting period is closed', request_id: 'r1' } }))
    const e = await api.get('/x').catch((x: unknown) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(friendly(e).title).toContain('دورهٔ بسته')
    expect(friendly(e).details).toBe('period_closed · r1')
  })

  it('reports a network failure with the friendly state', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('failed'))
    const e = await api.get('/x').catch((x: unknown) => x)
    expect(friendly(e).title).toBe('ارتباط با سرور برقرار نشد')
  })

  it('refreshes the session once on 401 and retries; a second 401 tells the app the session ended', async () => {
    const expired = vi.fn()
    const off = onSessionExpired(expired)
    fetchMock.mockResolvedValueOnce(json(401, { error: { code: 'unauthorized', message: 'x' } })).mockResolvedValueOnce(json(200, {})).mockResolvedValueOnce(json(200, { ok: 1 }))
    expect(await api.get('/v1/y')).toEqual({ ok: 1 })
    expect(expired).not.toHaveBeenCalled()
    fetchMock.mockResolvedValueOnce(json(401, { error: { code: 'unauthorized', message: 'x' } })).mockResolvedValueOnce(json(401, {})).mockResolvedValueOnce(json(401, { error: { code: 'unauthorized', message: 'x' } }))
    await api.get('/v1/y').catch(() => undefined)
    expect(expired).toHaveBeenCalledOnce()
    off()
  })
})
