import { Banner, Button, Heading, Link, Stack, Text } from '@primer/react'
import { SignOutIcon } from '@primer/octicons-react'

interface Props {
  onLogout: () => void
}

export default function HomePage({ onLogout }: Props) {
  return (
    <div style={{ maxWidth: 800, margin: '40px auto 0', padding: '0 16px' }}>
      <Stack direction="vertical" gap="spacious">
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Heading as="h1">Nipa</Heading>
          <Button onClick={onLogout} leadingVisual={<SignOutIcon />}>
            Sign out
          </Button>
        </div>

        <Banner variant="success" title="Signed in">
          Ready to build the repository browser, branches, history, diffs and
          merge requests on top of the growing REST API.
        </Banner>

        <Text as="p">
          Repositories, branches, history, diffs and merge requests will land
          here as the REST API surface grows. Primer React (GitHub&apos;s design
          system) is in use — see{' '}
          <Link href="https://primer.style" target="_blank" rel="noreferrer">
            primer.style
          </Link>
          .
        </Text>
      </Stack>
    </div>
  )
}