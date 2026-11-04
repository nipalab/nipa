import { useId, type ReactNode } from 'react'
import { VisuallyHidden } from '@primer/react'
import { DataTable as PrimerDataTable, Table, type Column, type UniqueRow } from '@primer/react/experimental'
import { EmptyState, Loading } from './ui'

export type { Column }

export function DataTable<T extends UniqueRow>({
  data,
  columns,
  label,
  empty = 'Nothing here yet.',
  loading = false,
  cellPadding = 'condensed',
  actions,
}: {
  data: T[]
  columns: Column<T>[]
  label: string
  empty?: ReactNode
  loading?: boolean
  cellPadding?: 'condensed' | 'normal' | 'spacious'
  actions?: ReactNode
}) {
  const titleId = useId()
  if (loading) return <Loading variant="skeleton" />
  if (data.length === 0) return <EmptyState>{empty}</EmptyState>
  return (
    <Table.Container>
      <VisuallyHidden>
        <Table.Title as="h3" id={titleId}>
          {label}
        </Table.Title>
      </VisuallyHidden>
      {actions && <Table.Actions>{actions}</Table.Actions>}
      <PrimerDataTable aria-labelledby={titleId} data={data} columns={columns} cellPadding={cellPadding} />
    </Table.Container>
  )
}
