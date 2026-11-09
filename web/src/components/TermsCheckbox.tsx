import { Checkbox, FormControl, Link as PrimerLink } from '@primer/react'

export const TERMS_URL = '/terms'
export const PRIVACY_URL = '/privacy'

export function TermsCheckbox({ checked, onChange }: { checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <FormControl>
      <Checkbox checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <FormControl.Label>
        I agree to the{' '}
        <PrimerLink href={TERMS_URL} target="_blank" rel="noopener">
          Terms of Service
        </PrimerLink>{' '}
        and{' '}
        <PrimerLink href={PRIVACY_URL} target="_blank" rel="noopener">
          Privacy Policy
        </PrimerLink>
      </FormControl.Label>
    </FormControl>
  )
}
