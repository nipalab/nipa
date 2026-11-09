import type { FormEventHandler, ReactNode } from 'react'
import { Heading, Stack } from '@primer/react'
import { Link } from 'react-router-dom'
import { NipaLogo } from './NipaLogo'

export function AuthLayout({
  title = 'Nipa',
  width = 360,
  children,
}: {
  title?: string
  width?: number
  children: ReactNode
}) {
  return (
    <div style={{ maxWidth: width, margin: 'var(--base-size-48) auto 0', padding: '0 var(--base-size-16)' }}>
      <Stack direction="vertical" gap="spacious" align="center">
        <Link to="/" aria-label="Nipa home">
          <NipaLogo size={48} />
        </Link>
        <Heading as="h1" style={{ fontSize: 24, fontWeight: 300, textAlign: 'center' }}>
          {title}
        </Heading>
        {children}
      </Stack>
    </div>
  )
}

export function AuthCard({
  onSubmit,
  noValidate = true,
  children,
}: {
  onSubmit: FormEventHandler<HTMLFormElement>
  noValidate?: boolean
  children: ReactNode
}) {
  return (
    <form
      onSubmit={onSubmit}
      noValidate={noValidate}
      style={{
        width: '100%',
        border: '1px solid var(--borderColor-default)',
        borderRadius: 'var(--borderRadius-medium)',
        padding: 'var(--base-size-24)',
      }}
    >
      <Stack direction="vertical" gap="normal">
        {children}
      </Stack>
    </form>
  )
}
