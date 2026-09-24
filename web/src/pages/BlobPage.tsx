import { Button, Link as PrimerLink, Stack, Text } from '@primer/react'
import { CopyIcon, DownloadIcon, FileBinaryIcon, LinkExternalIcon } from '@primer/octicons-react'
import { Suspense, lazy, useEffect, useState } from 'react'
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { fetchBlob } from '../api/endpoints'
import { BranchSelector } from '../components/repo/BranchSelector'
import { formatBytes } from '../components/repo/format'
import { RepoBreadcrumb } from '../components/repo/RepoBreadcrumb'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { blobUrl, commitsUrl } from '../components/repo/repoPaths'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading } from '../components/ui'
import { useAsync } from '../hooks'

const CodeView = lazy(() => import('../components/repo/CodeView').then((module) => ({ default: module.CodeView })))

const IMAGE_EXTENSIONS = new Set(['avif', 'bmp', 'gif', 'ico', 'jpeg', 'jpg', 'png', 'webp'])

function isImagePath(path: string): boolean {
  const dot = path.lastIndexOf('.')
  if (dot < 0) return false
  return IMAGE_EXTENSIONS.has(path.slice(dot + 1).toLowerCase())
}

function countLines(text: string): number {
  if (text === '') return 0
  const trimmed = text.endsWith('\n') ? text.slice(0, -1) : text
  return trimmed.split('\n').length
}

export default function BlobPage() {
  const { org = '', project = '' } = useParams()
  const params = useParams()
  const routeRev = params.rev ?? ''
  const splat = params['*'] ?? ''
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { branches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)

  const legacyRev = searchParams.get('rev') ?? ''
  const path = splat || searchParams.get('path') || ''
  const rev = routeRev || legacyRev

  const { data: file, error, loading } = useAsync(
    () => (path ? fetchBlob(org, project, rev, path) : Promise.resolve(null)),
    [org, project, rev, path],
  )

  const [text, setText] = useState<string | null>(null)
  const [imageUrl, setImageUrl] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    let active = true
    setText(null)
    if (file && !file.isBinary) {
      file.blob.text().then((value) => {
        if (active) setText(value)
      })
    }
    return () => {
      active = false
    }
  }, [file])

  useEffect(() => {
    if (!file || !file.isBinary || !isImagePath(path)) {
      setImageUrl(null)
      return
    }
    const url = URL.createObjectURL(file.blob)
    setImageUrl(url)
    return () => URL.revokeObjectURL(url)
  }, [file, path])

  if (legacyRev) {
    return <Navigate replace to={blobUrl(org, project, legacyRev, path)} />
  }

  const linkRev = rev || defaultBranch
  const name = path.split('/').pop() || path
  const lines = text === null ? 0 : countLines(text)

  const copy = async () => {
    if (text === null) return
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  const download = () => {
    if (!file) return
    const url = URL.createObjectURL(file.blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = name
    anchor.click()
    URL.revokeObjectURL(url)
  }

  const openRaw = () => {
    if (!file) return
    const url = URL.createObjectURL(file.blob)
    window.open(url, '_blank')
    setTimeout(() => URL.revokeObjectURL(url), 60_000)
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="code"
      rev={linkRev}
      canAdmin={canAdmin}
      canWrite={canWrite}
    >
      <Stack direction="horizontal" gap="normal" align="center" justify="space-between">
        <Stack direction="horizontal" gap="normal" align="center" style={{ minWidth: 0 }}>
          <BranchSelector
            branches={branches}
            rev={linkRev}
            onSelect={(nextRev) => navigate(blobUrl(org, project, nextRev, path))}
          />
          <RepoBreadcrumb org={org} project={project} rev={linkRev} path={path} leafIsFile />
        </Stack>
        <Stack direction="horizontal" gap="condensed" align="center">
          <Text style={{ color: 'var(--fgColor-muted)', whiteSpace: 'nowrap' }}>
            {text !== null ? `${lines} lines` : ''}
            {file ? `${text !== null ? ' · ' : ''}${formatBytes(file.size)}` : ''}
          </Text>
          <PrimerLink as={Link} to={commitsUrl(org, project, linkRev, path)} style={{ whiteSpace: 'nowrap' }}>
            History
          </PrimerLink>
          {text !== null && (
            <Button size="small" leadingVisual={CopyIcon} onClick={copy}>
              {copied ? 'Copied' : 'Copy'}
            </Button>
          )}
          {file && (
            <Button size="small" leadingVisual={DownloadIcon} onClick={download}>
              Download
            </Button>
          )}
          {file && (
            <Button size="small" leadingVisual={LinkExternalIcon} onClick={openRaw}>
              Raw
            </Button>
          )}
        </Stack>
      </Stack>

      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && file && (
        <div
          style={{
            border: '1px solid var(--borderColor-default)',
            borderRadius: 6,
            overflow: 'hidden',
            background: 'var(--bgColor-default)',
          }}
        >
          {text !== null && (
            <Suspense fallback={<Loading />}>
              <CodeView code={text} path={path} />
            </Suspense>
          )}
          {file.isBinary && imageUrl && (
            <div style={{ padding: 24, textAlign: 'center' }}>
              <img
                src={imageUrl}
                alt={name}
                style={{ maxWidth: '100%', maxHeight: 600, borderRadius: 6, background: 'var(--bgColor-muted)' }}
              />
            </div>
          )}
          {file.isBinary && !imageUrl && (
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                gap: 8,
                padding: 48,
                color: 'var(--fgColor-muted)',
              }}
            >
              <FileBinaryIcon size={32} />
              <Text>Binary file — preview is not available.</Text>
            </div>
          )}
        </div>
      )}
    </RepoPageShell>
  )
}
