export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]*$/

export function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}
