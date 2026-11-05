import { ApiError } from '../api/client'
import { createOrg } from '../api/endpoints'
import { RESERVED_ORG_SLUGS } from '../slug'
import { FormDialog } from './FormDialog'
import { SlugFields, useSlugFields } from './SlugFields'

export function CreateOrgDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (slug: string) => void }) {
  const slugFields = useSlugFields(RESERVED_ORG_SLUGS)

  return (
    <FormDialog
      title="New organization"
      width="large"
      submitLabel="Create organization"
      submitDisabled={!slugFields.valid}
      onClose={onClose}
      onSubmit={async () => {
        try {
          const org = await createOrg(slugFields.name.trim(), slugFields.slug)
          onCreated(org.slug)
        } catch (err) {
          if (err instanceof ApiError && err.status === 409) {
            slugFields.markTaken()
            return false
          }
          throw err
        }
      }}
    >
      <SlugFields state={slugFields} autoFocus />
    </FormDialog>
  )
}
