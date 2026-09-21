import { Heading, Link as PrimerLink, Stack, Text } from '@primer/react'
import { Link } from 'react-router-dom'
import { listOrgs } from '../api/endpoints'
import { useAuth } from '../auth'
import { EmptyState, ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function HomePage() {
  const { me } = useAuth()
  const { data: orgs, error, loading } = useAsync(listOrgs, [])

  return (
    <Page title="Repositories" subtitle={`Signed in as ${me?.email ?? ''}`}>
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && orgs && orgs.length === 0 && (
        <EmptyState>You are not a member of any organization yet.</EmptyState>
      )}
      {orgs?.map((org) => (
        <div
          key={org.id}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <Stack direction="vertical" gap="condensed">
            <Heading as="h3">
              <PrimerLink as={Link} to={`/${org.slug}`}>
                {org.name}
              </PrimerLink>
            </Heading>
            <Text style={{ color: 'var(--fgColor-muted)' }}>
              {org.slug} · role: {org.role}
            </Text>
          </Stack>
        </div>
      ))}
    </Page>
  )
}
