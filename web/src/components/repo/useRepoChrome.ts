import { getMyProjectPermissions, listBranches } from '../../api/endpoints'
import { PERMISSION_ADMIN, PERMISSION_WRITE } from '../../api/models'
import { useAuth } from '../../auth'
import { useAsync } from '../../hooks'

export function useRepoChrome(org: string, project: string) {
  const { me } = useAuth()
  const {
    data: branches,
    error: branchesError,
    loading: branchesLoading,
    reload: reloadBranches,
  } = useAsync(() => listBranches(org, project), [org, project])
  const { data: permissions } = useAsync(
    () => getMyProjectPermissions(org, project),
    [org, project],
  )
  const canWrite = Boolean(
    me?.is_admin || me?.is_super_admin || ((permissions?.project_permission ?? 0) & PERMISSION_WRITE) !== 0,
  )
  const canAdmin = Boolean(
    me?.is_admin || me?.is_super_admin || ((permissions?.project_permission ?? 0) & PERMISSION_ADMIN) !== 0,
  )
  const defaultBranch = branches?.find((branch) => branch.is_default)?.name ?? ''
  return {
    branches,
    branchesError,
    branchesLoading,
    reloadBranches,
    canWrite,
    canAdmin,
    defaultBranch,
  }
}
