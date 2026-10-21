// Password policy discovery. The hub enforces the policy and is its single
// source of truth (chart and hub can be upgraded independently, so the hint
// must not be duplicated into the frontend runtime-config). The hub's 400 on
// set remains the contract; the client-side hint is advisory only.
import { useQuery } from '@tanstack/react-query'
import { request } from './client'

export type PasswordPolicy = {
  minLength: number
  maxLength: number
  requireCharacterClasses: string[]
}

// Historical hard-coded policy. Used as the fallback when the hub does not
// serve the endpoint (old hub) or the request fails, so the hint degrades to
// exactly the text shown before the policy was configurable.
export const DEFAULT_PASSWORD_POLICY: PasswordPolicy = {
  minLength: 8,
  maxLength: 128,
  requireCharacterClasses: [],
}

export async function getPasswordPolicy(): Promise<PasswordPolicy> {
  const raw = await request<unknown>('/auth/password-policy')
  if (typeof raw !== 'object' || raw === null) {
    throw new Error('invalid password policy response')
  }
  const o = raw as Record<string, unknown>
  if (typeof o.minLength !== 'number' || typeof o.maxLength !== 'number') {
    throw new Error('invalid password policy response')
  }
  return {
    minLength: o.minLength,
    maxLength: o.maxLength,
    requireCharacterClasses: Array.isArray(o.requireCharacterClasses)
      ? o.requireCharacterClasses.filter((c): c is string => typeof c === 'string')
      : [],
  }
}

// Mirrors the hub's class vocabulary (services/hub/internal/api/
// password_policy.go): lower, upper, digit, symbol (ASCII printable
// non-alphanumeric). Keep both sides in sync.
const CLASS_HINTS: Record<string, string> = {
  lower: '1 lowercase letter',
  upper: '1 uppercase letter',
  digit: '1 digit',
  symbol: '1 symbol',
}

export function passwordPolicyHint(policy: PasswordPolicy): string {
  const parts = [`at least ${policy.minLength} characters`]
  for (const cls of policy.requireCharacterClasses) {
    parts.push(CLASS_HINTS[cls] ?? `1 ${cls} character`)
  }
  const text = parts.join('; ')
  return text.charAt(0).toUpperCase() + text.slice(1) + '.'
}

// Hint text for password forms. Falls back to "At least 8 characters." —
// the pre-policy wording — while loading or on any failure.
export function usePasswordPolicyHint(): string {
  const { data } = useQuery({
    queryKey: ['password-policy'],
    queryFn: getPasswordPolicy,
    staleTime: 5 * 60 * 1000,
  })
  return passwordPolicyHint(data ?? DEFAULT_PASSWORD_POLICY)
}
