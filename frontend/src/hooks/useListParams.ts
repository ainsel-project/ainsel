import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useUrlFilters, type StringFilters } from './useUrlFilters'

/** Rows-per-page choices the list pages offer the Pager. */
export const PAGE_SIZE_OPTIONS = [10, 20, 50] as const

// The keys every list page owns in the URL, before a page adds its own.
const BASE_KEYS = ['page', 'pageSize', 'search'] as const
const NO_EXTRA_KEYS: readonly string[] = []

/** A URL write. `undefined` and `''` remove the key. */
export type ListParamPatch = Record<string, string | number | undefined>

export type UseListParamsOptions = {
  /** Rows-per-page choices offered by the Pager (default `[10, 20, 50]`). */
  pageSizes?: readonly number[]
  /** Page size when the URL has none, or names one the page does not offer (default 20). */
  defaultPageSize?: number
  /**
   * Extra URL keys the page owns — `['groupId']`, `['tags', 'orderBy']`. Pass a
   * module-level constant: the array is a hook dependency, so a fresh literal on
   * every render would rebuild the filter object each time.
   */
  extraKeys?: readonly string[]
  /** How long `setSearch` waits before it writes the URL (default 300 ms). */
  searchDebounceMs?: number
}

export type ListParams = {
  page: number
  pageSize: number
  /** The choices to hand the Pager, so a page never re-declares them. */
  pageSizes: readonly number[]
  /** The committed search term — what the list query filters on. */
  search: string
  /** What the search input shows. The URL follows it `searchDebounceMs` later. */
  searchDraft: string
  setPage: (page: number) => void
  setPageSize: (pageSize: number) => void
  /** Records the keystroke; the URL — and therefore the query — follows debounced. */
  setSearch: (value: string) => void
  /** Raw URL values, including `extraKeys`. */
  filters: StringFilters
  /** Write filters; returns the caller to page 1 unless the patch names a page. */
  setFilters: (patch: ListParamPatch) => void
  /** Drop every list key from the URL, the search draft included. */
  reset: () => void
}

/**
 * useListParams is the URL state a list page needs: page, page size and a
 * debounced search, all built on `useUrlFilters` so one caller writes the URL.
 *
 * Paging and page size live in the URL only — a reload or a shared link lands
 * on the same page. Search is typed, so it goes through a draft: `setSearch`
 * records the keystroke and the URL catches up after `searchDebounceMs`, which
 * is what keeps a keystroke from firing a request.
 *
 * Every write that changes *what* is listed — search, a filter, a sort — drops
 * `page`, because page 7 of the old result set is not page 7 of the new one.
 */
export function useListParams(options: UseListParamsOptions = {}): ListParams {
  const {
    pageSizes = PAGE_SIZE_OPTIONS,
    defaultPageSize = 20,
    extraKeys = NO_EXTRA_KEYS,
    searchDebounceMs = 300,
  } = options

  const keys = useMemo(() => [...BASE_KEYS, ...extraKeys], [extraKeys])
  const {
    filters,
    setFilters: setUrlFilters,
    reset: resetUrlFilters,
  } = useUrlFilters<StringFilters>(keys)

  const page = Math.max(1, Number(filters.page) || 1)
  const requestedPageSize = Number(filters.pageSize)
  const pageSize = (pageSizes as readonly number[]).includes(requestedPageSize)
    ? requestedPageSize
    : defaultPageSize
  const search = filters.search ?? ''

  const write = useCallback(
    (patch: ListParamPatch) => {
      const next: StringFilters = {}
      for (const [key, value] of Object.entries(patch)) {
        next[key] = value === undefined || value === '' ? undefined : String(value)
      }
      setUrlFilters(next)
    },
    [setUrlFilters],
  )

  const setPage = useCallback((next: number) => write({ page: Math.max(1, next) }), [write])

  const setPageSize = useCallback(
    (next: number) => write({ pageSize: next, page: undefined }),
    [write],
  )

  const setFilters = useCallback(
    (patch: ListParamPatch) => write('page' in patch ? patch : { ...patch, page: undefined }),
    [write],
  )

  // --- search: a draft the URL catches up with, never the other way round ---
  const [searchDraft, setSearchDraft] = useState(search)
  const draftRef = useRef(searchDraft)
  draftRef.current = searchDraft
  // The value the debounce last put into the URL. `search` moving without it
  // means something else moved the URL — back/forward, a reset — and the input
  // should follow, unless an edit is still in flight.
  const committedRef = useRef(search)

  const commitSearch = useCallback(
    (value: string) => {
      committedRef.current = value
      write({ search: value, page: undefined })
    },
    [write],
  )

  // The debounce must not restart when `write` changes identity, or a page that
  // re-renders faster than `searchDebounceMs` would never commit its search.
  const commitRef = useRef(commitSearch)
  useEffect(() => {
    commitRef.current = commitSearch
  })

  const setSearch = useCallback((value: string) => setSearchDraft(value), [])

  useEffect(() => {
    if (searchDraft === search) return
    const timer = setTimeout(() => commitRef.current(searchDraft), searchDebounceMs)
    return () => clearTimeout(timer)
  }, [searchDraft, search, searchDebounceMs])

  useEffect(() => {
    if (draftRef.current !== committedRef.current) return
    setSearchDraft(search)
    committedRef.current = search
  }, [search])

  const reset = useCallback(() => {
    committedRef.current = ''
    setSearchDraft('')
    resetUrlFilters()
  }, [resetUrlFilters])

  return {
    page,
    pageSize,
    pageSizes,
    search,
    searchDraft,
    setPage,
    setPageSize,
    setSearch,
    filters,
    setFilters,
    reset,
  }
}
