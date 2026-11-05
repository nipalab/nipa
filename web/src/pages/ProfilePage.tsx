import { useState } from 'react'
import { Banner, Button, Checkbox, FormControl, TextInput } from '@primer/react'
import { useSearchParams } from 'react-router-dom'
import { changeMyPassword, updateMyProfile } from '../api/endpoints'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/ActorAvatar'
import { IdentitiesCard } from '../components/IdentitiesCard'
import { SettingsCard, SettingsRow } from '../components/SettingsCard'
import { SSHKeysCard } from '../components/SSHKeysCard'
import { isUserTab, UserSettingsLayout, type UserTab } from '../components/UserSettingsLayout'
import { ErrorBanner } from '../components/ui'
import { errorMessage } from '../hooks'

export default function ProfilePage() {
  const [params] = useSearchParams()
  const requested = params.get('tab')
  const oauthReturn = params.has('oauth') || params.has('oauth_error')
  const tab: UserTab = isUserTab(requested) && requested !== 'billing' ? requested : oauthReturn ? 'accounts' : 'profile'

  return (
    <UserSettingsLayout active={tab}>
      {tab === 'profile' && <ProfileTab />}
      {tab === 'password' && <PasswordTab />}
      {tab === 'accounts' && <IdentitiesCard />}
      {tab === 'keys' && <SSHKeysCard />}
    </UserSettingsLayout>
  )
}

function ProfileTab() {
  const { me, refreshMe } = useAuth()
  const [name, setName] = useState(me?.name ?? '')
  const [photoUrl, setPhotoUrl] = useState(me?.photo_url ?? '')
  const [notifyEmail, setNotifyEmail] = useState(me?.notify_email ?? true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (name.trim() === '' || saving) return
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      await updateMyProfile(name.trim(), photoUrl.trim(), notifyEmail)
      await refreshMe()
      setNotice('Profile updated.')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingsCard
        title="Public profile"
        footer={
          <Button type="submit" variant="primary" loading={saving} disabled={name.trim() === '' || saving}>
            Update profile
          </Button>
        }
      >
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          {error && <ErrorBanner error={error} />}
          {notice && <Banner variant="success" title={notice} onDismiss={() => setNotice(null)} />}
        </div>
        <SettingsRow label="Profile picture" subtext="Shown next to your comments, reviews and commits.">
          <div style={{ display: 'flex', gap: 'var(--base-size-16)', alignItems: 'flex-start' }}>
            <ActorAvatar
              actor={{ user_id: me?.id ?? '', name: me?.name, photo_url: photoUrl || me?.photo_url }}
              size={100}
            />
            <div style={{ flex: 1 }}>
              <FormControl>
                <FormControl.Label>Photo URL</FormControl.Label>
                <TextInput block placeholder="https://…" value={photoUrl} onChange={(event) => setPhotoUrl(event.target.value)} />
              </FormControl>
            </div>
          </div>
        </SettingsRow>
        <SettingsRow label="Name" subtext="Your name may appear where you contribute or are mentioned.">
          <FormControl required>
            <FormControl.Label visuallyHidden>Name</FormControl.Label>
            <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
          </FormControl>
        </SettingsRow>
        <SettingsRow label="Email notifications">
          <FormControl>
            <Checkbox checked={notifyEmail} onChange={(event) => setNotifyEmail(event.target.checked)} />
            <FormControl.Label>Email notifications</FormControl.Label>
            <FormControl.Caption>
              Receive email about merge requests you participate in, as author, reviewer, assignee or commenter.
              Notifications are sent to {me?.email ?? 'your email address'}.
            </FormControl.Caption>
          </FormControl>
        </SettingsRow>
      </SettingsCard>
    </form>
  )
}

function PasswordTab() {
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const tooShort = newPassword !== '' && newPassword.length < 8
  const mismatch = confirmPassword !== '' && newPassword !== confirmPassword
  const canSave = oldPassword !== '' && newPassword.length >= 8 && newPassword === confirmPassword && !saving

  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (!canSave) return
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      await changeMyPassword(oldPassword, newPassword)
      setOldPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setNotice('Password updated.')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingsCard
        title="Change password"
        footer={
          <Button type="submit" variant="primary" loading={saving} disabled={!canSave}>
            Update password
          </Button>
        }
      >
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          {error && <ErrorBanner error={error} />}
          {notice && <Banner variant="success" title={notice} onDismiss={() => setNotice(null)} />}
        </div>
        <SettingsRow label="Old password">
          <FormControl required>
            <FormControl.Label visuallyHidden>Old password</FormControl.Label>
            <TextInput block type="password" value={oldPassword} onChange={(event) => setOldPassword(event.target.value)} />
          </FormControl>
        </SettingsRow>
        <SettingsRow label="New password" subtext="At least 8 characters.">
          <FormControl required>
            <FormControl.Label visuallyHidden>New password</FormControl.Label>
            <TextInput block type="password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} />
            {tooShort && (
              <FormControl.Validation variant="error">Password must be at least 8 characters.</FormControl.Validation>
            )}
          </FormControl>
        </SettingsRow>
        <SettingsRow label="Confirm new password">
          <FormControl required>
            <FormControl.Label visuallyHidden>Confirm new password</FormControl.Label>
            <TextInput
              block
              type="password"
              value={confirmPassword}
              onChange={(event) => setConfirmPassword(event.target.value)}
            />
            {mismatch && <FormControl.Validation variant="error">Passwords do not match.</FormControl.Validation>}
          </FormControl>
        </SettingsRow>
      </SettingsCard>
    </form>
  )
}
