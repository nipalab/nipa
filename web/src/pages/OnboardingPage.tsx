import { useState } from 'react'
import { Button } from '@primer/react'
import { Navigate, useNavigate } from 'react-router-dom'
import { ApiError } from '../api/client'
import { createOrg, listOrgs } from '../api/endpoints'
import { AuthCard, AuthLayout } from '../components/AuthLayout'
import { SlugFields, useSlugFields } from '../components/SlugFields'
import { ErrorBanner, Loading, SectionHeading } from '../components/ui'
import { errorMessage, useAsync } from '../hooks'
import { RESERVED_ORG_SLUGS } from '../slug'
import { APP_HOME } from '../routes'

// OnboardingPage is the shared first-run step after registration confirmation
// and social signup: name the first workspace. Users who already belong to an
// organization skip it.
export default function OnboardingPage() {
  const navigate = useNavigate()
  const { data: orgs, error: loadError, loading } = useAsync(listOrgs, [])
  const slugFields = useSlugFields(RESERVED_ORG_SLUGS)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (loading) {
    return (
      <AuthLayout title="Welcome to Nipa" width={420}>
        <Loading />
      </AuthLayout>
    )
  }
  if (loadError) {
    return (
      <AuthLayout title="Welcome to Nipa" width={420}>
        <ErrorBanner error={loadError} />
      </AuthLayout>
    )
  }
  if (orgs && orgs.length > 0) {
    return <Navigate to={APP_HOME} replace />
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!slugFields.valid || busy) return
    setBusy(true)
    setError(null)
    try {
      const org = await createOrg(slugFields.name.trim(), slugFields.slug)
      navigate(`/${org.slug}`, { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        slugFields.markTaken()
      } else {
        setError(errorMessage(err))
      }
      setBusy(false)
    }
  }

  return (
    <AuthLayout title="Welcome to Nipa" width={420}>
      <AuthCard onSubmit={handleSubmit} noValidate={false}>
        <SectionHeading>Name your workspace</SectionHeading>
        <SlugFields
          state={slugFields}
          nameLabel="Organization name"
          namePlaceholder="e.g. Acme Game Studio"
          nameCaption="You become its owner and can invite members and create projects afterwards."
          slugPlaceholder="acme-game-studio"
          slugCaption={`Your workspace URL: /${slugFields.slug || '…'}`}
          autoFocus
        />

        <ErrorBanner error={error} />

        <Button type="submit" variant="primary" block loading={busy} disabled={busy || !slugFields.valid}>
          {busy ? 'Creating…' : 'Create workspace'}
        </Button>
      </AuthCard>
    </AuthLayout>
  )
}
