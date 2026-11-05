import { Stack, Text } from '@primer/react'
import { ArrowRightIcon } from '@primer/octicons-react'
import { useParams, useSearchParams } from 'react-router-dom'
import { getCommitDiff } from '../api/endpoints'
import { BranchSelector } from '../components/repo/BranchSelector'
import { DiffView } from '../components/repo/DiffView'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { useTags } from '../components/repo/useTags'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
import { useAsync } from '../hooks'

export default function ComparePage() {
  const { org = '', project = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const { branches, branchesLoading, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { tags, load: loadTags } = useTags(org, project)

  const base = params.get('base') ?? defaultBranch
  const head = params.get('head') ?? ''
  const resolve = (ref: string) => branches?.find((branch) => branch.name === ref)?.commit_id ?? ref
  const baseId = base ? resolve(base) : ''
  const headId = head ? resolve(head) : ''
  const ready = !branchesLoading && baseId !== '' && headId !== ''
  const same = ready && baseId === headId

  const { data: diff, error, loading } = useAsync(
    () => (ready && !same ? getCommitDiff(org, project, headId, baseId) : Promise.resolve(null)),
    [org, project, baseId, headId, ready, same],
  )

  function select(key: 'base' | 'head', value: string) {
    const next = new URLSearchParams(params)
    next.set(key, value)
    if (!next.has('base') && defaultBranch) next.set('base', defaultBranch)
    setParams(next, { replace: true })
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="commits"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="Compare changes"
      crumbs={[{ label: 'Branches', to: `/${org}/${project}/branches` }, { label: 'Compare' }]}
    >
      <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)' }}>
        Choose two branches, tags or commits to see what changed from the base to the compare side.
      </Text>
      <Stack direction="horizontal" gap="normal" align="center" wrap="wrap">
        <div style={{ minWidth: 200 }}>
          <BranchSelector
            branches={branches}
            rev={base}
            onSelect={(value) => select('base', value)}
            tags={tags}
            onOpen={loadTags}
            onSelectTag={(tag) => select('base', tag.commit_id)}
            loading={branchesLoading}
          />
        </div>
        <ArrowRightIcon />
        <div style={{ minWidth: 200 }}>
          <BranchSelector
            branches={branches}
            rev={head}
            placeholder="choose to compare"
            onSelect={(value) => select('head', value)}
            tags={tags}
            onOpen={loadTags}
            onSelectTag={(tag) => select('head', tag.commit_id)}
            loading={branchesLoading}
          />
        </div>
      </Stack>

      <ErrorBanner error={error} />
      {!head && <EmptyState>Choose a branch, tag or commit to compare with the base.</EmptyState>}
      {same && <EmptyState>Both sides point to the same commit, so there is nothing to compare.</EmptyState>}
      {loading && <Loading />}
      {diff && diff.files.length === 0 && <EmptyState>There are no differences between these revisions.</EmptyState>}
      {diff && diff.files.length > 0 && <DiffView files={diff.files} />}
    </RepoPageShell>
  )
}
