import type { ComponentType } from 'react'

export type TocEntry = { level: number; text: string; id: string }

// A docs page written as a React component instead of markdown. List its slug
// in docs/_sidebar.md like any other doc; DocsPage renders the component in
// the content column instead of fetching `<slug>.md`, and keeps the sidebar,
// search, TOC and prev/next working around it.
export type ComponentPage = {
  component: ComponentType
  // Plain text the docs search matches against (there is no .md to fetch).
  searchText: string
  // Drives the in-page TOC; ids must match the component's h2/h3 ids.
  headings: TocEntry[]
}

export const componentPages: Record<string, ComponentPage> = {}
