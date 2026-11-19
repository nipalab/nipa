import { Banner } from '@primer/react'
import { Link } from 'react-router-dom'
import { ApiError } from '../../api/client'
import { getOnboardingCapabilities } from '../../api/endpoints'
import { useAsync } from '../../hooks'

export function useByoStorage(): { enabled: boolean; loading: boolean } {
  const { data, loading } = useAsync(
    () =>
      Promise.resolve()
        .then(() => getOnboardingCapabilities())
        .catch(() => null),
    [],
  )
  return { enabled: Boolean(data?.byo_storage), loading }
}

export function isOwnStorageError(err: unknown): err is ApiError {
  return err instanceof ApiError && err.status === 402 && /own storage/i.test(err.message)
}

export function OwnStorageBanner() {
  return (
    <Banner variant="warning" title="Requires the Own storage add-on">
      Registering, changing or using your own storage servers needs the Own storage add-on.{' '}
      <Link to="/settings/billing">Add it in billing settings</Link>.
    </Banner>
  )
}

interface CheckInfo {
  label: string
  explanation: string
}

const CHECKS: Record<string, CheckInfo> = {
  open: {
    label: 'Connection',
    explanation: 'Nipa could not connect with these settings. Check the endpoint URL (public HTTPS), region and keys.',
  },
  bucket: {
    label: 'Bucket access',
    explanation:
      'The bucket could not be reached. Check the bucket name and region, and that the access key may list it (s3:ListBucket).',
  },
  signed_content_length: {
    label: 'Signed upload length',
    explanation:
      'The server accepted an upload longer than its signed Content-Length. Nipa needs SigV4 presigned uploads that enforce the signed length.',
  },
  presigned_put: {
    label: 'Presigned upload',
    explanation: 'A presigned upload failed. Check that the access key may write objects under the prefix (s3:PutObject).',
  },
  conditional_create: {
    label: 'Create-only upload',
    explanation:
      'The server ignores If-None-Match: * on presigned uploads, so stored content could be overwritten. AWS S3, MinIO and Cloudflare R2 support it.',
  },
  presigned_get: {
    label: 'Presigned download',
    explanation: 'A presigned download failed. Check that the access key may read objects (s3:GetObject).',
  },
  stat: {
    label: 'Object metadata',
    explanation: 'Reading the test object back failed or reported the wrong size (HeadObject).',
  },
  delete: {
    label: 'Delete',
    explanation: 'The test object could not be deleted. Check that the access key may delete objects (s3:DeleteObject).',
  },
}

export function checkInfo(check: string): CheckInfo {
  return CHECKS[check] ?? { label: check, explanation: 'The storage server failed this check.' }
}

export function probeCheckOf(err: unknown): string | null {
  if (!(err instanceof ApiError) || err.status !== 400) return null
  const match = /^storage server check (\S+) failed/.exec(err.message)
  return match ? match[1] : null
}

export function ProbeFailureBanner({ check, detail }: { check: string; detail?: string }) {
  const info = checkInfo(check)
  return (
    <Banner variant="critical" title={`Check failed: ${info.label}`}>
      {info.explanation}
      {detail && (
        <div style={{ marginTop: 'var(--base-size-4)', color: 'var(--fgColor-muted)', fontSize: 12, overflowWrap: 'anywhere' }}>
          {detail}
        </div>
      )}
    </Banner>
  )
}
