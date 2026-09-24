const EXTENSIONS: Record<string, string> = {
  c: 'c',
  cc: 'cpp',
  cjs: 'javascript',
  coffee: 'coffeescript',
  cpp: 'cpp',
  cs: 'clike',
  css: 'css',
  cxx: 'cpp',
  gql: 'graphql',
  go: 'go',
  graphql: 'graphql',
  h: 'c',
  hh: 'cpp',
  hpp: 'cpp',
  htm: 'markup',
  html: 'markup',
  java: 'clike',
  js: 'javascript',
  json: 'json',
  jsonc: 'json',
  jsx: 'jsx',
  kt: 'kotlin',
  kts: 'kotlin',
  less: 'css',
  m: 'objectivec',
  markdown: 'markdown',
  md: 'markdown',
  mjs: 'javascript',
  mm: 'objectivec',
  mts: 'typescript',
  py: 'python',
  rs: 'rust',
  sass: 'css',
  scss: 'css',
  sql: 'sql',
  svg: 'markup',
  swift: 'swift',
  ts: 'typescript',
  tsx: 'tsx',
  vue: 'markup',
  webmanifest: 'json',
  xml: 'markup',
  yaml: 'yaml',
  yml: 'yaml',
}

export function languageFor(path: string): string {
  const name = path.split('/').pop() ?? path
  const dot = name.lastIndexOf('.')
  if (dot < 0) {
    return 'plain'
  }
  return EXTENSIONS[name.slice(dot + 1).toLowerCase()] ?? 'plain'
}
