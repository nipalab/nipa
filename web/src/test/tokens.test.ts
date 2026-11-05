import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const SRC = resolve(__dirname, '..')
const TOKEN_PREFIXES = ['--base-size-', '--base-text-', '--borderRadius-', '--fontStack-', '--text-', '--stack-']

function importedCss(): string {
  const main = readFileSync(join(SRC, 'main.tsx'), 'utf8')
  const files = [...main.matchAll(/import '(@primer\/primitives\/[^']+\.css)'/g)].map((match) => match[1])
  return files.map((file) => readFileSync(resolve(SRC, '..', 'node_modules', file), 'utf8')).join('\n')
}

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'test' ? [] : sources(path)
    return /\.(tsx?|css)$/.test(name) && !name.endsWith('.test.tsx') ? [path] : []
  })
}

describe('design tokens', () => {
  it('only uses size/typography tokens that main.tsx loads', () => {
    const defined = new Set([...importedCss().matchAll(/(--[\w-]+)\s*:/g)].map((match) => match[1]))
    const missing: string[] = []
    for (const file of sources(SRC)) {
      const text = readFileSync(file, 'utf8')
      for (const match of text.matchAll(/var\((--[\w-]+)(,[^)]*)?\)/g)) {
        const [, name, fallback] = match
        if (fallback || !TOKEN_PREFIXES.some((prefix) => name.startsWith(prefix))) continue
        if (!defined.has(name)) missing.push(`${file.slice(SRC.length + 1)}: ${name}`)
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
