import { useState } from 'react'
import { Button, Link as PrimerLink } from '@primer/react'
import { Link, useNavigate } from 'react-router-dom'
import { listOrgs } from '../api/endpoints'
import { useAuth } from '../auth'
import { CreateOrgDialog } from '../components/CreateOrgDialog'
import { Row, RowList } from '../components/RowList'
import { EmptyState, ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function HomePage() {
  const { me } = useAuth()
  const navigate = useNavigate()
  const { data: orgs, error, loading } = useAsync(listOrgs, [])
  const [showCreate, setShowCreate] = useState(false)

  return (
    <Page
      title="Organizations"
      subtitle={`Signed in as ${me?.email ?? ''}`}
      actions={
        <Button variant="primary" onClick={() => setShowCreate(true)}>
          New organization
        </Button>
      }
    >
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && orgs && orgs.length === 0 && (
        <EmptyState>You are not a member of any organization yet.</EmptyState>
      )}
      {orgs && orgs.length > 0 && (
        <RowList>
          {orgs.map((org, index) => (
            <Row
              key={org.id}
              first={index === 0}
              title={
                <PrimerLink as={Link} to={`/${org.slug}`}>
                  {org.name}
                </PrimerLink>
              }
              meta={`${org.slug} · role: ${org.role}`}
            />
          ))}
        </RowList>
      )}
      {showCreate && (
        <CreateOrgDialog onClose={() => setShowCreate(false)} onCreated={(slug) => navigate(`/${slug}`)} />
      )}
    </Page>
  )
}
