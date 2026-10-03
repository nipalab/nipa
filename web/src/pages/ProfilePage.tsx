import { useState } from 'react'
import type { CSSProperties } from 'react'
import { Button, Flash, FormControl, Heading, Stack, TextInput } from '@primer/react'
import { changeMyPassword, updateMyProfile } from '../api/endpoints'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/repo/ActorAvatar'
import { ErrorBanner, Page, PRIMARY_BUTTON_STYLE } from '../components/ui'

const CARD: CSSProperties = {
  border: '1px solid var(--borderColor-default)',
  borderRadius: 6,
  padding: 16,
  scrollMarginTop: 16,
}

export default function ProfilePage() {
  const { me, refreshMe } = useAuth()
  const [name, setName] = useState(me?.name ?? '')
  const [photoUrl, setPhotoUrl] = useState(me?.photo_url ?? '')
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [savingProfile, setSavingProfile] = useState(false)
  const [savingPassword, setSavingPassword] = useState(false)

  const passwordTooShort = newPassword !== '' && newPassword.length < 8
  const passwordMismatch = confirmPassword !== '' && newPassword !== confirmPassword
  const canSavePassword =
    oldPassword !== '' && newPassword.length >= 8 && newPassword === confirmPassword && !savingPassword

  async function saveProfile(event: React.FormEvent) {
    event.preventDefault()
    if (name.trim() === '' || savingProfile) return
    setSavingProfile(true)
    setError(null)
    setNotice(null)
    try {
      await updateMyProfile(name.trim(), photoUrl.trim())
      await refreshMe()
      setNotice('Profile updated.')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSavingProfile(false)
    }
  }

  async function savePassword(event: React.FormEvent) {
    event.preventDefault()
    if (!canSavePassword) return
    setSavingPassword(true)
    setError(null)
    setNotice(null)
    try {
      await changeMyPassword(oldPassword, newPassword)
      setOldPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setNotice('Password updated.')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSavingPassword(false)
    }
  }

  return (
    <Page title="Public profile" subtitle={me?.email}>
      <ErrorBanner error={error} />
      {notice && <Flash variant="success">{notice}</Flash>}
      <div className="nipa-settings-layout">
        <nav className="nipa-settings-nav" aria-label="Profile settings">
          <a href="#public-profile">Public profile</a>
          <a href="#password">Password and authentication</a>
        </nav>
        <div className="nipa-settings-main">
          <form id="public-profile" onSubmit={saveProfile} style={CARD}>
            <Stack direction="vertical" gap="normal">
              <Heading as="h3" style={{ margin: 0, fontSize: 16 }}>
                Profile picture
              </Heading>
              <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start' }}>
                <ActorAvatar
                  actor={{ user_id: me?.id ?? '', name: me?.name, photo_url: photoUrl || me?.photo_url }}
                  size={100}
                />
                <div style={{ flex: 1 }}>
                  <FormControl>
                    <FormControl.Label>Photo URL</FormControl.Label>
                    <TextInput
                      block
                      placeholder="https://…"
                      value={photoUrl}
                      onChange={(event) => setPhotoUrl(event.target.value)}
                    />
                    <FormControl.Caption>
                      Your avatar is shown next to your comments, reviews and commits.
                    </FormControl.Caption>
                  </FormControl>
                </div>
              </div>
              <FormControl required>
                <FormControl.Label>Name</FormControl.Label>
                <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
                <FormControl.Caption>
                  Your name may appear where you contribute or are mentioned.
                </FormControl.Caption>
              </FormControl>
              <div style={{ borderTop: '1px solid var(--borderColor-muted)', paddingTop: 12 }}>
                <Button
                  type="submit"
                  variant="primary"
                  style={PRIMARY_BUTTON_STYLE}
                  loading={savingProfile}
                  disabled={name.trim() === '' || savingProfile}
                >
                  Update profile
                </Button>
              </div>
            </Stack>
          </form>

          <form id="password" onSubmit={savePassword} style={CARD}>
            <Stack direction="vertical" gap="normal">
              <Heading as="h3" style={{ margin: 0, fontSize: 16 }}>
                Change password
              </Heading>
              <FormControl required>
                <FormControl.Label>Old password</FormControl.Label>
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
                <FormControl.Caption>At least 8 characters.</FormControl.Caption>
                {passwordTooShort && (
                  <FormControl.Validation variant="error">
                    Password must be at least 8 characters.
                  </FormControl.Validation>
                )}
              </FormControl>
              <FormControl required>
                <FormControl.Label>Confirm new password</FormControl.Label>
                <TextInput
                  block
                  type="password"
                  value={confirmPassword}
                  onChange={(event) => setConfirmPassword(event.target.value)}
                />
                {passwordMismatch && (
                  <FormControl.Validation variant="error">Passwords do not match.</FormControl.Validation>
                )}
              </FormControl>
              <div style={{ borderTop: '1px solid var(--borderColor-muted)', paddingTop: 12 }}>
                <Button
                  type="submit"
                  variant="primary"
                  style={PRIMARY_BUTTON_STYLE}
                  loading={savingPassword}
                  disabled={!canSavePassword}
                >
                  Update password
                </Button>
              </div>
            </Stack>
          </form>
        </div>
      </div>
    </Page>
  )
}
