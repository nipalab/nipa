ALTER TABLE refresh_tokens DROP COLUMN mfa;

ALTER TABLE refresh_tokens DROP COLUMN auth_method;

DROP TABLE mfa_login_challenges;

DROP TABLE mfa_recovery_codes;

DROP TABLE user_totp;
