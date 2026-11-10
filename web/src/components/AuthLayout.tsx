import type { FormEventHandler, ReactNode } from 'react'
import { Heading, Link as PrimerLink, Stack } from '@primer/react'
import { Link } from 'react-router-dom'
import { NipaLogo } from './NipaLogo'
import { PRIVACY_URL, TERMS_URL } from './TermsCheckbox'

const FOOTER_LINKS = [
  { label: 'Terms', href: TERMS_URL },
  { label: 'Privacy', href: PRIVACY_URL },
  { label: 'Docs', href: '/guides' },
  { label: 'Contact', href: '/contact' },
]

// AuthFooter links the hosted service's legal and help pages; self-hosted
// servers do not serve them, so callers show it only in SaaS mode.
export function AuthFooter() {
  return (
    <nav aria-label="Legal and help">
      <Stack direction="horizontal" gap="normal" justify="center" wrap="wrap">
        {FOOTER_LINKS.map((link) => (
          <PrimerLink key={link.href} href={link.href} muted style={{ fontSize: 12 }}>
            {link.label}
          </PrimerLink>
        ))}
      </Stack>
    </nav>
  )
}

// AuthAside is the bordered box under the form ("New to Nipa? ...").
export function AuthAside({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        width: '100%',
        boxSizing: 'border-box',
        border: '1px solid var(--borderColor-default)',
        borderRadius: 'var(--borderRadius-medium)',
        padding: 'var(--base-size-16)',
        textAlign: 'center',
        fontSize: 14,
      }}
    >
      {children}
    </div>
  )
}

export function AuthLayout({
  title = 'Nipa',
  width = 360,
  footer = false,
  children,
}: {
  title?: string
  width?: number
  footer?: boolean
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
        {footer && <AuthFooter />}
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
