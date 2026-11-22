import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { DataTable, Select, type Column } from '.'

function Harness({ onChange }: { onChange(v: string): void }) {
  const [v, setV] = useState('a')
  return (
    <Select
      label="انتخاب"
      value={v}
      onChange={(x) => (setV(x), onChange(x))}
      options={[{ value: 'a', label: 'بانک ملت' }, { value: 'b', label: 'بانک پاسارگاد' }, { value: 'c', label: 'صندوق ۱۱۱' }]}
    />
  )
}

describe('Select', () => {
  it('filters while typing (Persian and Latin digits), moves with arrows and picks with Enter', async () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    const box = screen.getByRole('combobox', { name: 'انتخاب' })
    await userEvent.click(box)
    expect(screen.getAllByRole('option')).toHaveLength(3)
    await userEvent.type(box, 'بانک')
    expect(screen.getAllByRole('option')).toHaveLength(2)
    await userEvent.keyboard('{ArrowDown}{Enter}')
    expect(onChange).toHaveBeenCalledWith('b')
  })
  it('matches digits typed in Latin against Persian labels and shows the empty text', async () => {
    render(<Harness onChange={() => undefined} />)
    const box = screen.getByRole('combobox')
    await userEvent.click(box)
    await userEvent.type(box, '111')
    expect(screen.getAllByRole('option')).toHaveLength(1)
    await userEvent.clear(box)
    await userEvent.type(box, 'zzz')
    expect(screen.getByText('موردی پیدا نشد')).toBeInTheDocument()
  })
  it('closes on Escape without changing the value', async () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    await userEvent.click(screen.getByRole('combobox'))
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(onChange).not.toHaveBeenCalled()
  })
})

interface Row { id: string; name: string }
const cols: Column<Row>[] = [{ id: 'name', header: 'نام', filter: 'name', cell: (r) => r.name }]

describe('DataTable shortcuts', () => {
  const rows = [{ id: '1', name: 'الف' }, { id: '2', name: 'ب' }]
  it('Ctrl+F toggles the column-filter row, handled on the focused element inside the table', () => {
    render(<DataTable caption="t" columns={cols} rows={rows} rowKey={(r) => r.id} />)
    const row = screen.getAllByRole('row')[1]!
    expect(screen.queryByLabelText('فیلتر نام')).toBeNull()
    fireEvent.keyDown(row, { key: 'f', ctrlKey: true })
    expect(screen.getByLabelText('فیلتر نام')).toBeInTheDocument()
    fireEvent.keyDown(row, { key: 'F', ctrlKey: true })
    expect(screen.queryByLabelText('فیلتر نام')).toBeNull()
  })
  it('Ctrl+M turns on multi-select and reports the selection', async () => {
    const onSelection = vi.fn()
    render(<DataTable caption="t" columns={cols} rows={rows} rowKey={(r) => r.id} selectable onSelection={onSelection} />)
    fireEvent.keyDown(screen.getAllByRole('row')[1]!, { key: 'm', ctrlKey: true })
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes).toHaveLength(2)
    await userEvent.click(boxes[0]!)
    expect(onSelection).toHaveBeenLastCalledWith([rows[0]])
  })
  it('Ctrl+H asks for the history of the focused row', () => {
    const onHistory = vi.fn()
    render(<DataTable caption="t" columns={cols} rows={rows} rowKey={(r) => r.id} onHistory={onHistory} />)
    const row = screen.getAllByRole('row')[2]!
    fireEvent.focus(row)
    fireEvent.keyDown(row, { key: 'h', ctrlKey: true })
    expect(onHistory).toHaveBeenCalledWith(rows[1])
  })
  it('shows the empty state with a way to clear filters', () => {
    render(<DataTable caption="t" columns={cols} rows={[]} rowKey={(r) => r.id} filters={{ name: 'x' }} onFilters={() => undefined} />)
    expect(within(document.body).getByText('نتیجه‌ای پیدا نشد')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'پاک‌کردن فیلترها' })).toBeInTheDocument()
  })
})
