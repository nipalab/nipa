import { describe, expect, it } from 'vitest'
import { MAX_PASSWORD_BYTES, passwordBytes, passwordLengthProblem, passwordProblem } from './PasswordFields'

describe('password length policy', () => {
  it('counts UTF-8 bytes, not UTF-16 code units', () => {
    expect(passwordBytes('abc')).toBe(3)
    expect(passwordBytes('é')).toBe(2)
    expect(passwordBytes('€')).toBe(3)
    expect(passwordBytes('😀')).toBe(4)
    expect('😀'.length).toBe(2)
  })

  it('accepts 72 bytes and refuses 73', () => {
    expect(passwordLengthProblem('a'.repeat(MAX_PASSWORD_BYTES))).toBeNull()
    expect(passwordLengthProblem('a'.repeat(MAX_PASSWORD_BYTES + 1))).toContain('at most 72 bytes')
    expect(passwordLengthProblem('€'.repeat(24))).toBeNull()
    expect(passwordLengthProblem('a'.repeat(71) + 'é')).toContain('at most 72 bytes')
    expect(passwordLengthProblem('😀'.repeat(19))).toContain('at most 72 bytes')
  })

  it('keeps the minimum and the confirmation check', () => {
    expect(passwordLengthProblem('short')).toContain('at least 8 characters')
    expect(passwordProblem('é'.repeat(37), 'é'.repeat(37))).toContain('at most 72 bytes')
    expect(passwordProblem('password1', 'password2')).toBe('The passwords do not match.')
    expect(passwordProblem('password1', 'password1')).toBeNull()
  })
})
