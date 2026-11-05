import { describe, expect, it } from 'vitest'
import { RESERVED_ORG_SLUGS, RESERVED_PROJECT_SLUGS, SLUG_PATTERN, slugify } from './slug'

describe('slug', () => {
  it('derives a slug from the name', () => {
    expect(slugify(' Acme Game Studio ')).toBe('acme-game-studio')
  })

  it('reserves web UI and marketing paths for organizations', () => {
    for (const slug of ['web', 'login', 'dashboard', 'settings', 'api', 'docs', 'debug', 'pricing', 'screenshots', 'version', 'contact', 'download', 'guides', 'privacy', 'support', 'terms']) {
      expect(RESERVED_ORG_SLUGS.has(slug)).toBe(true)
    }
    expect(RESERVED_ORG_SLUGS.has('acme')).toBe(false)
    expect(SLUG_PATTERN.test('acme')).toBe(true)
  })

  it('reserves second-segment project routes', () => {
    expect(RESERVED_PROJECT_SLUGS.has('new')).toBe(true)
    expect(RESERVED_PROJECT_SLUGS.has('settings')).toBe(true)
    expect(RESERVED_PROJECT_SLUGS.has('game')).toBe(false)
  })
})
