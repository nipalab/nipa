import { useState } from 'react'
import { Button, FormControl, Stack, TextInput } from '@primer/react'
import { changeMyPassword, updateMyProfile } from '../api/endpoints'
import { useAuth } from '../auth'
import { ErrorBanner, Page } from '../components/ui'

export default function ProfilePage() {
  const { me, refreshMe } = useAuth()
  const [name, setName] = useState(me?.name ?? '')
  const [photoUrl, setPhotoUrl] = useState(me?.photo_url ?? '')
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>, message: string) {
    setError(null)
    setNotice(null)
    try {
      await action()
      setNotice(message)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Page title="Profile" subtitle={me?.email}>
      <ErrorBanner error={error} />
      {notice && <div style={{ color: 'var(--fgColor-success)' }}>{notice}</div>}

      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            await updateMyProfile(name, photoUrl)
            await refreshMe()
          }, 'Profile updated')
        }}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>Profile</strong>
          <FormControl required>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
          </FormControl>
          <FormControl>
            <FormControl.Label>Photo URL</FormControl.Label>
            <TextInput block value={photoUrl} onChange={(event) => setPhotoUrl(event.target.value)} />
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Save profile
          </Button>
        </Stack>
      </form>

      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            await changeMyPassword(oldPassword, newPassword)
            setOldPassword('')
            setNewPassword('')
          }, 'Password updated')
        }}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>Change password</strong>
          <FormControl required>
            <FormControl.Label>Current password</FormControl.Label>
            <TextInput
              block
              type="password"
              value={oldPassword}
              onChange={(event) => setOldPassword(event.target.value)}
            />
          </FormControl>
          <FormControl required>
            <FormControl.Label>New password</FormControl.Label>
            <TextInput
              block
              type="password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Change password
          </Button>
        </Stack>
      </form>
    </Page>
  )
}
