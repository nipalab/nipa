export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]*$/

// First-segment paths owned by the SPA router, the marketing site or fixed
// HTTP endpoints; an organization with one of these slugs would be shadowed.
export const RESERVED_ORG_SLUGS = new Set([
  'admin',
  'api',
  'assets',
  'auth',
  'dashboard',
  'debug',
  'docs',
  'healthz',
  'login',
  'metrics',
  'oauth',
  'onboarding',
  'readyz',
  'register',
  'settings',
  'verify',
  'version',
  'web',
])

// Second-segment route literals under /{org}/ in the SPA.
export const RESERVED_PROJECT_SLUGS = new Set(['new', 'settings'])

export function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}
