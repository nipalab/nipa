export const PROVIDER_LABEL: Record<string, string> = {
  github: 'GitHub',
  google: 'Google',
}

export function providerLabel(provider: string): string {
  return PROVIDER_LABEL[provider] ?? provider
}

export const OAUTH_SIGNIN_ERRORS: Record<string, string> = {
  email_conflict:
    'An account with this email already exists. Sign in with your password (or its original provider), then connect this provider from your profile.',
  email_required:
    'The provider did not share a verified email address. Verify an email with the provider or register with your email and password.',
  identity_taken: 'This provider account is already linked to another Nipa account.',
}

export const OAUTH_LINK_ERRORS: Record<string, string> = {
  email_conflict: 'An account with this email already exists.',
  email_required: 'The provider did not share a verified email address.',
  identity_taken: 'This provider account is already linked to another Nipa account.',
}
