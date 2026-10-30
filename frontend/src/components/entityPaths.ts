// Route builders for entity detail pages. Kept out of EntityLink.tsx so that
// file exports only components (react-refresh).
export const agentPath = (id: string) => `/agents/${encodeURIComponent(id)}`
export const agentImagePath = (id: string) => `/agent-images/${encodeURIComponent(id)}`
export const connectorPath = (id: string) => `/connectors/${encodeURIComponent(id)}`
export const groupPath = (id: string) => `/groups/${encodeURIComponent(id)}`
export const eventPath = (id: string) => `/observability/events/${encodeURIComponent(id)}`
