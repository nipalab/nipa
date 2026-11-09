import { ActionMenu, Text } from '@primer/react'
import { CodeIcon, TerminalIcon } from '@primer/octicons-react'
import { CopyButton } from '../CopyButton'
import { repoUrl } from './repoPaths'

export function cloneUrl(org: string, project: string, origin = window.location.origin): string {
  return `${origin}${repoUrl(org, project)}`
}

function CopyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="nipa-clone-field">
      <code>{value}</code>
      <CopyButton value={value} label={label} />
    </div>
  )
}

// CloneButton is the GitHub-style green "Code" button: it shows the clone URL
// and the nipa CLI command, each with a copy button.
export function CloneButton({ org, project }: { org: string; project: string }) {
  const url = cloneUrl(org, project)
  return (
    <ActionMenu>
      <ActionMenu.Button variant="primary" leadingVisual={CodeIcon}>
        Code
      </ActionMenu.Button>
      <ActionMenu.Overlay width="large" align="end">
        <div className="nipa-clone-panel">
          <Text as="h3" className="nipa-clone-title">
            <TerminalIcon /> Clone
          </Text>
          <Text as="p" className="nipa-clone-hint">
            Clone with the nipa CLI:
          </Text>
          <CopyField label="Copy clone command" value={`nipa clone ${url}`} />
          <Text as="p" className="nipa-clone-hint">
            Repository URL, also for <strong>Clone</strong> in the Nipa desktop app:
          </Text>
          <CopyField label="Copy repository URL" value={url} />
          <Text as="p" className="nipa-clone-hint">
            Add <code>--sparse assets,docs</code> to check out only some folders.
          </Text>
        </div>
      </ActionMenu.Overlay>
    </ActionMenu>
  )
}
