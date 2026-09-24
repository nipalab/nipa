function encodeSegment(value: string): string {
  return encodeURIComponent(value)
}

function encodePath(path: string): string {
  return path
    .split('/')
    .filter(Boolean)
    .map(encodeSegment)
    .join('/')
}

export function repoUrl(org: string, project: string): string {
  return `/${encodeSegment(org)}/${encodeSegment(project)}`
}

export function treeUrl(org: string, project: string, rev: string, path = ''): string {
  const base = `${repoUrl(org, project)}/tree`
  if (!rev) {
    return path ? `${base}?path=${encodeSegment(path)}` : base
  }
  const suffix = path ? `/${encodePath(path)}` : ''
  return `${base}/${encodeSegment(rev)}${suffix}`
}

export function blobUrl(org: string, project: string, rev: string, path: string): string {
  const base = `${repoUrl(org, project)}/blob`
  if (!rev) {
    return `${base}?path=${encodeSegment(path)}`
  }
  return `${base}/${encodeSegment(rev)}/${encodePath(path)}`
}

export function commitsUrl(org: string, project: string, rev = '', path = ''): string {
  const params = new URLSearchParams()
  if (rev) params.set('branch', rev)
  if (path) params.set('path', path)
  const query = params.toString()
  return `${repoUrl(org, project)}/commits${query ? `?${query}` : ''}`
}

export function parentPath(path: string): string {
  const segments = path.split('/').filter(Boolean)
  segments.pop()
  return segments.join('/')
}
