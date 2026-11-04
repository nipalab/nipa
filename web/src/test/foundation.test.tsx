import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BookIcon } from '@primer/octicons-react'
import { DataTable } from '../components/DataTable'
import { FormDialog } from '../components/FormDialog'
import { SettingsCard, SettingsRow } from '../components/SettingsCard'
import { SettingsLayout } from '../components/SettingsLayout'
import { SlugFields, slugMessage, useSlugFields } from '../components/SlugFields'
import { StateBadge, stateLabel, stateVariant } from '../components/StateBadge'
import { Timestamp } from '../components/Timestamp'
import { useConfirmAction } from '../components/confirm'
import { EmptyState, ErrorBanner, Loading, Mono, NoPermission, Page, SectionHeading, StatusLabel } from '../components/ui'
import { RESERVED_ORG_SLUGS } from '../slug'
import { buttonByText, click, flush, render, type } from './render'

afterEach(() => {
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('Page', () => {
  it('renders title, subtitle, actions and content', async () => {
    const { container } = await render(
      <Page title="Projects" subtitle="All of them" actions={<button>New</button>}>
        <p>body</p>
      </Page>,
    )
    expect(container.querySelector('h2')?.textContent).toBe('Projects')
    expect(container.textContent).toContain('All of them')
    expect(buttonByText(container, 'New')).not.toBeNull()
    expect(container.textContent).toContain('body')
  })

  it('renders only children when there is no title or actions', async () => {
    const { container } = await render(
      <Page>
        <p>just body</p>
      </Page>,
    )
    expect(container.querySelector('h2')).toBeNull()
    expect(container.textContent).toBe('just body')
  })

  it('renders breadcrumbs with links except for the current crumb', async () => {
    const { container } = await render(
      <Page title="Settings" breadcrumbs={[{ label: 'acme', to: '/acme' }, { label: 'game', to: '/acme/game' }, { label: 'Settings' }]}>
        <p>x</p>
      </Page>,
    )
    const nav = container.querySelector('nav[aria-label="Breadcrumb"]')
    expect(nav).not.toBeNull()
    const hrefs = Array.from(nav?.querySelectorAll('a') ?? []).map((a) => a.getAttribute('href'))
    expect(hrefs).toContain('/acme')
    expect(hrefs).toContain('/acme/game')
    expect(nav?.textContent).toContain('Settings')
  })

  it('omits the breadcrumb nav for an empty list', async () => {
    const { container } = await render(
      <Page title="t" breadcrumbs={[]}>
        <p>x</p>
      </Page>,
    )
    expect(container.querySelector('nav[aria-label="Breadcrumb"]')).toBeNull()
  })
})

describe('SectionHeading', () => {
  it('renders heading, description and actions', async () => {
    const { container } = await render(
      <SectionHeading description="About" actions={<button>Add</button>}>
        Members
      </SectionHeading>,
    )
    expect(container.querySelector('h3')?.textContent).toBe('Members')
    expect(container.textContent).toContain('About')
    expect(buttonByText(container, 'Add')).not.toBeNull()
  })

  it('honours the heading level', async () => {
    const { container } = await render(<SectionHeading as="h2">Top</SectionHeading>)
    expect(container.querySelector('h2')?.textContent).toBe('Top')
  })
})

describe('ErrorBanner', () => {
  it('renders nothing without an error', async () => {
    const { container } = await render(<ErrorBanner error={null} />)
    expect(container.textContent).toBe('')
  })

  it('renders a titled banner and supports dismissal', async () => {
    const onDismiss = vi.fn()
    const { container } = await render(<ErrorBanner error="Bad things" title="Oops" onDismiss={onDismiss} />)
    expect(container.textContent).toContain('Oops')
    expect(container.textContent).toContain('Bad things')
    await click(container.querySelector('button[class*="BannerDismiss"]'))
    expect(onDismiss).toHaveBeenCalled()
  })

  it('defaults the title to Request failed', async () => {
    const { container } = await render(<ErrorBanner error="x" />)
    expect(container.textContent).toContain('Request failed')
  })
})

describe('Loading / EmptyState / NoPermission', () => {
  it('renders a spinner by default and a skeleton on request', async () => {
    const spinner = await render(<Loading />)
    expect(spinner.container.querySelector('[role="status"]')).toBeNull()
    const skeleton = await render(<Loading variant="skeleton" lines={2} />)
    expect(skeleton.container.querySelector('[role="status"]')).not.toBeNull()
  })

  it('renders a blankslate with optional icon, description and action', async () => {
    const { container } = await render(
      <EmptyState icon={BookIcon} description="Create one" action={<button>Create</button>}>
        No repos
      </EmptyState>,
    )
    expect(container.textContent).toContain('No repos')
    expect(container.textContent).toContain('Create one')
    expect(container.querySelector('svg')).not.toBeNull()
    expect(buttonByText(container, 'Create')).not.toBeNull()
  })

  it('renders a bare blankslate', async () => {
    const { container } = await render(<EmptyState>Nothing</EmptyState>)
    expect(container.textContent).toBe('Nothing')
  })

  it('shows a spinner while permissions load and the message afterwards', async () => {
    const loading = await render(<NoPermission loading />)
    expect(loading.container.textContent).not.toContain('permission')
    const done = await render(<NoPermission>Owners only</NoPermission>)
    expect(done.container.textContent).toContain("You don't have permission")
    expect(done.container.textContent).toContain('Owners only')
    const fallback = await render(<NoPermission />)
    expect(fallback.container.textContent).toContain('Ask an administrator')
  })

  it('Mono and StatusLabel render their text', async () => {
    const { container } = await render(
      <>
        <Mono>abc123</Mono>
        <StatusLabel status="behind_target" />
        <StatusLabel status="merged" />
        <StatusLabel status="closed" />
        <StatusLabel status="open" />
      </>,
    )
    expect(container.textContent).toContain('abc123')
    expect(container.textContent).toContain('behind target')
  })
})

describe('SettingsCard / SettingsRow', () => {
  it('renders header, rows and footer', async () => {
    const { container } = await render(
      <SettingsCard title="Branches" description="Protect them" actions={<button>Add</button>} footer={<button>Save</button>}>
        <SettingsRow label="main" subtext="default">
          <span>value</span>
        </SettingsRow>
        <SettingsRow label="label only" />
      </SettingsCard>,
    )
    expect(container.querySelector('h2')?.textContent).toBe('Branches')
    expect(container.textContent).toContain('Protect them')
    expect(container.textContent).toContain('default')
    expect(container.textContent).toContain('value')
    expect(container.textContent).toContain('label only')
    expect(buttonByText(container, 'Add')).not.toBeNull()
    expect(buttonByText(container, 'Save')).not.toBeNull()
  })

  it('renders without footer or children', async () => {
    const { container } = await render(<SettingsCard title="Empty" />)
    expect(container.querySelector('h2')?.textContent).toBe('Empty')
    expect(container.querySelectorAll('button')).toHaveLength(0)
  })
})

describe('SettingsLayout', () => {
  it('marks the active item, hides hidden ones and shows children', async () => {
    const { container } = await render(
      <SettingsLayout
        active="b"
        label="Project settings"
        items={[
          { key: 'a', label: 'Alpha', to: '/x?tab=a', icon: BookIcon },
          { key: 'b', label: 'Beta', to: '/x?tab=b' },
          { key: 'c', label: 'Gamma', to: '/x?tab=c', hidden: true },
        ]}
      >
        <p>panel</p>
      </SettingsLayout>,
    )
    expect(container.querySelector('nav[aria-label="Project settings"]')).not.toBeNull()
    const links = Array.from(container.querySelectorAll('a'))
    expect(links.map((a) => a.textContent)).toEqual(['Alpha', 'Beta'])
    expect(links[1].getAttribute('aria-current')).toBe('page')
    expect(links[0].getAttribute('aria-current')).toBeNull()
    expect(container.textContent).toContain('panel')
  })
})

describe('DataTable', () => {
  const columns = [
    { header: 'Name', field: 'name' as const, rowHeader: true },
    { id: 'size', header: 'Size', renderCell: (row: { id: number; name: string; size: number }) => <span>{row.size} B</span> },
  ]

  it('renders rows and an accessible title', async () => {
    const { container } = await render(
      <DataTable
        label="Files"
        columns={columns}
        data={[
          { id: 1, name: 'a.txt', size: 10 },
          { id: 2, name: 'b.txt', size: 20 },
        ]}
        actions={<button>Export</button>}
      />,
    )
    const table = container.querySelector('table')
    expect(table).not.toBeNull()
    expect(table?.getAttribute('aria-labelledby')).toBeTruthy()
    expect(container.textContent).toContain('a.txt')
    expect(container.textContent).toContain('20 B')
    expect(container.textContent).toContain('Files')
    expect(buttonByText(container, 'Export')).not.toBeNull()
  })

  it('shows the empty message for no rows', async () => {
    const { container } = await render(<DataTable label="Files" columns={columns} data={[]} empty="No files" />)
    expect(container.querySelector('table')).toBeNull()
    expect(container.textContent).toContain('No files')
  })

  it('shows a skeleton while loading', async () => {
    const { container } = await render(<DataTable label="Files" columns={columns} data={[]} loading />)
    expect(container.querySelector('[role="status"]')).not.toBeNull()
    expect(container.querySelector('table')).toBeNull()
  })

  it('falls back to the default empty message', async () => {
    const { container } = await render(<DataTable label="Files" columns={columns} data={[]} />)
    expect(container.textContent).toContain('Nothing here yet.')
  })
})

describe('Timestamp', () => {
  it('renders relative text with an absolute tooltip', async () => {
    const iso = new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString()
    const { container } = await render(<Timestamp value={iso} />)
    const time = container.querySelector('time')
    expect(time?.getAttribute('datetime')).toBe(iso)
    expect(time?.getAttribute('title')).toBeTruthy()
    expect(time?.textContent).toMatch(/hours? ago/)
  })

  it('renders the absolute value on request', async () => {
    const iso = '2026-01-02T03:04:05Z'
    const { container } = await render(<Timestamp value={iso} mode="absolute" />)
    expect(container.querySelector('time')?.textContent).toBe(new Date(iso).toLocaleString())
  })

  it('renders the fallback for missing or invalid values', async () => {
    expect((await render(<Timestamp value={undefined} fallback="never" />)).container.textContent).toBe('never')
    expect((await render(<Timestamp value={null} />)).container.textContent).toBe('')
    expect((await render(<Timestamp value="not a date" fallback="?" />)).container.textContent).toBe('?')
  })
})

describe('StateBadge', () => {
  it('maps statuses to variants', () => {
    expect(stateVariant('merged')).toBe('done')
    expect(stateVariant('OPEN')).toBe('accent')
    expect(stateVariant('mergeable')).toBe('success')
    expect(stateVariant('failed')).toBe('danger')
    expect(stateVariant('behind_target')).toBe('attention')
    expect(stateVariant('whatever')).toBe('default')
  })

  it('humanizes labels', async () => {
    expect(stateLabel('behind_target')).toBe('Behind target')
    const { container } = await render(
      <>
        <StateBadge status="merged" />
        <StateBadge status="open">Custom</StateBadge>
      </>,
    )
    expect(container.textContent).toContain('Merged')
    expect(container.textContent).toContain('Custom')
  })
})

describe('slug fields', () => {
  it('validates slugs', () => {
    expect(slugMessage('', RESERVED_ORG_SLUGS)).toBe('Slug is required.')
    expect(slugMessage('Bad Slug', RESERVED_ORG_SLUGS)).toContain('must start')
    expect(slugMessage('api', RESERVED_ORG_SLUGS)).toContain('reserved')
    expect(slugMessage('good-slug', RESERVED_ORG_SLUGS)).toBeNull()
  })

  function Harness({ onState }: { onState: (state: ReturnType<typeof useSlugFields>) => void }) {
    const state = useSlugFields(RESERVED_ORG_SLUGS)
    onState(state)
    return <SlugFields state={state} nameLabel="Workspace" autoFocus />
  }

  it('derives the slug from the name until it is edited, and flags taken slugs', async () => {
    let latest!: ReturnType<typeof useSlugFields>
    const { container } = await render(<Harness onState={(state) => (latest = state)} />)
    const [nameInput, slugInput] = Array.from(container.querySelectorAll('input'))
    expect(container.textContent).toContain('Workspace')
    expect(latest.valid).toBe(false)

    await type(nameInput, 'My Team')
    expect(latest.slug).toBe('my-team')
    expect((slugInput as HTMLInputElement).value).toBe('my-team')
    expect(latest.valid).toBe(true)

    await type(slugInput, 'custom')
    await type(nameInput, 'Another Name')
    expect(latest.slug).toBe('custom')

    await type(slugInput, 'api')
    expect(latest.valid).toBe(false)
    expect(container.textContent).toContain('reserved')

    await type(slugInput, 'taken')
    await new Promise<void>((resolve) => {
      latest.markTaken()
      resolve()
    })
    await flush()
    expect(container.textContent).toContain('already taken')
    await type(slugInput, 'free')
    expect(container.textContent).not.toContain('already taken')
  })
})

describe('FormDialog', () => {
  it('submits, then closes', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()
    await render(
      <FormDialog title="Add thing" onSubmit={onSubmit} onClose={onClose} submitLabel="Add">
        <input aria-label="field" />
      </FormDialog>,
    )
    expect(document.body.textContent).toContain('Add thing')
    await click(buttonByText(document.body, 'Add'))
    await flush()
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('keeps the dialog open and shows the error when submit fails', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('server said no'))
    const onClose = vi.fn()
    await render(
      <FormDialog title="Add thing" onSubmit={onSubmit} onClose={onClose}>
        <input aria-label="field" />
      </FormDialog>,
    )
    await click(buttonByText(document.body, 'Save'))
    await flush()
    expect(onClose).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('server said no')
    expect(document.body.textContent).toContain('Could not save')
  })

  it('does not submit while disabled and cancels on request', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()
    await render(
      <FormDialog title="Danger" onSubmit={onSubmit} onClose={onClose} submitDisabled submitVariant="danger">
        <span>x</span>
      </FormDialog>,
    )
    await click(buttonByText(document.body, 'Save'))
    expect(onSubmit).not.toHaveBeenCalled()
    await click(buttonByText(document.body, 'Cancel'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('ignores submit while a request is in flight', async () => {
    let release: () => void = () => undefined
    const onSubmit = vi.fn(() => new Promise<void>((resolve) => (release = resolve)))
    const onClose = vi.fn()
    await render(
      <FormDialog title="Slow" onSubmit={onSubmit} onClose={onClose}>
        <span>x</span>
      </FormDialog>,
    )
    await click(document.body.querySelector('button[type="submit"]'))
    await click(document.body.querySelector('button[type="submit"]'))
    expect(onSubmit).toHaveBeenCalledTimes(1)
    release()
    await flush()
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe('useConfirmAction', () => {
  function Harness({ onResult, danger }: { onResult: (value: boolean) => void; danger?: boolean }) {
    const confirm = useConfirmAction()
    const [label] = useState('Delete branch')
    return (
      <button
        onClick={() =>
          void confirm({ title: label, content: 'This cannot be undone.', confirmLabel: 'Delete', danger }).then(onResult)
        }
      >
        open
      </button>
    )
  }

  it('resolves true when confirmed', async () => {
    const onResult = vi.fn()
    const { container } = await render(<Harness onResult={onResult} />)
    await click(buttonByText(container, 'open'))
    await flush()
    expect(document.body.textContent).toContain('This cannot be undone.')
    await click(buttonByText(document.body, 'Delete'))
    await flush()
    expect(onResult).toHaveBeenCalledWith(true)
  })

  it('resolves false when cancelled', async () => {
    const onResult = vi.fn()
    const { container } = await render(<Harness onResult={onResult} danger={false} />)
    await click(buttonByText(container, 'open'))
    await flush()
    await click(buttonByText(document.body, 'Cancel'))
    await flush()
    expect(onResult).toHaveBeenCalledWith(false)
  })
})
