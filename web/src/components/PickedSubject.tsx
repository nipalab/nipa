import { Button, Label } from '@primer/react'
import { PeopleIcon } from '@primer/octicons-react'
import { ActorAvatar } from './ActorAvatar'
import type { PickerEntry } from './UserGroupPicker'

// PickedSubject shows the user or group chosen in a picker, with a way to
// pick again.
export function PickedSubject({ subject, onChange }: { subject: PickerEntry; onChange: () => void }) {
  return (
    <div className="nipa-picked-subject" aria-label="Selected user or group">
      {subject.isGroup ? (
        <span className="nipa-picked-group">
          <PeopleIcon />
        </span>
      ) : (
        <ActorAvatar actor={{ user_id: subject.id, name: subject.label, photo_url: subject.avatarUrl }} size={32} />
      )}
      <span style={{ minWidth: 0, flex: 1 }}>
        <span className="nipa-picked-name">{subject.label}</span>
        <span className="nipa-picked-sub">{subject.sublabel}</span>
      </span>
      <Label variant="secondary">{subject.isGroup ? 'Group' : 'User'}</Label>
      <Button size="small" onClick={onChange}>
        Change
      </Button>
    </div>
  )
}
