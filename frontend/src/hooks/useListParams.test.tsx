import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { useListParams, type ListParams } from './useListParams'

// Renders the hook and records the URL it writes, the way the list pages use it.
function renderListParams(initial: string, extraKeys?: readonly string[]) {
  let lastSearch = ''
  let api: ListParams

  function Probe() {
    api = useListParams(extraKeys ? { extraKeys } : undefined)
    return null
  }
  function Spy() {
    lastSearch = useLocation().search
    return null
  }

  render(
    <MemoryRouter initialEntries={[initial]}>
      <Probe />
      <Spy />
    </MemoryRouter>,
  )
  return {
    api: () => api,
    url: () => lastSearch,
  }
}

const commit = (ms = 300) =>
  act(() => {
    vi.advanceTimersByTime(ms)
  })

describe('useListParams', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('falls back to page 1 and the default page size', () => {
    const h = renderListParams('/agents')
    expect(h.api().page).toBe(1)
    expect(h.api().pageSize).toBe(20)
    expect(h.api().pageSizes).toEqual([10, 20, 50])
    expect(h.api().search).toBe('')
  })

  it('reads page and page size from the URL', () => {
    const h = renderListParams('/agents?page=3&pageSize=50')
    expect(h.api().page).toBe(3)
    expect(h.api().pageSize).toBe(50)
  })

  it('ignores a page size the page does not offer', () => {
    const h = renderListParams('/agents?pageSize=999')
    expect(h.api().pageSize).toBe(20)
  })

  it('clamps a page below one', () => {
    const h = renderListParams('/agents?page=0')
    expect(h.api().page).toBe(1)
  })

  it('keeps the page when paging', () => {
    const h = renderListParams('/agents')
    act(() => h.api().setPage(4))
    expect(h.url()).toContain('page=4')
  })

  it('returns to page 1 when the page size changes', () => {
    const h = renderListParams('/agents?page=4')
    act(() => h.api().setPageSize(50))
    expect(h.url()).toContain('pageSize=50')
    expect(h.url()).not.toContain('page=')
  })

  it('debounces the search and drops the page with it', () => {
    const h = renderListParams('/agents?page=4')
    act(() => h.api().setSearch('triage'))
    expect(h.api().searchDraft).toBe('triage')
    expect(h.api().search).toBe('')
    expect(h.url()).toContain('page=4')

    commit()
    expect(h.api().search).toBe('triage')
    expect(h.url()).toContain('search=triage')
    expect(h.url()).not.toContain('page=')
  })

  it('commits one search for a burst of keystrokes', () => {
    const h = renderListParams('/agents')
    act(() => h.api().setSearch('t'))
    commit(100)
    act(() => h.api().setSearch('tr'))
    commit(100)
    expect(h.url()).not.toContain('search=')
    commit(200)
    expect(h.url()).toContain('search=tr')
  })

  it('removes the search param when the input is cleared', () => {
    const h = renderListParams('/agents?search=triage')
    act(() => h.api().setSearch(''))
    commit()
    expect(h.api().search).toBe('')
    expect(h.url()).not.toContain('search=')
  })

  it('follows a search the URL moved on its own', () => {
    const h = renderListParams('/agents?search=triage')
    expect(h.api().searchDraft).toBe('triage')
  })

  it('writes extra keys through setFilters and resets the page', () => {
    const h = renderListParams('/agents?page=4', ['groupId'])
    act(() => h.api().setFilters({ groupId: 'g-platform' }))
    expect(h.api().filters.groupId).toBe('g-platform')
    expect(h.url()).toContain('groupId=g-platform')
    expect(h.url()).not.toContain('page=')
  })

  it('keeps a page the patch names itself', () => {
    const h = renderListParams('/agents', ['orderBy'])
    act(() => h.api().setFilters({ orderBy: 'name', page: 2 }))
    expect(h.url()).toContain('orderBy=name')
    expect(h.url()).toContain('page=2')
  })

  it('removes a filter written as an empty string', () => {
    const h = renderListParams('/agents?groupId=g-platform', ['groupId'])
    act(() => h.api().setFilters({ groupId: '' }))
    expect(h.url()).not.toContain('groupId=')
  })

  it('clears the list keys and the search draft on reset', () => {
    const h = renderListParams('/agents?page=2&search=triage&keep=1')
    act(() => h.api().reset())
    expect(h.api().searchDraft).toBe('')
    expect(h.url()).toBe('?keep=1')
  })
})
