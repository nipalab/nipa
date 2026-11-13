package usecase

import (
	"context"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
)

// EnvOTP supplies the two-factor code to a password login that has no terminal.
const EnvOTP = "NIPA_OTP"

// LoginHint is the hint of logins that cannot prompt for what they need.
const LoginHint = "run nipa login in a terminal or log in with an SSH key"

const maxOTPPrompts = 3

type mfaExecutor interface {
	CompleteLoginMFA(ctx context.Context, host, challenge, code string) (*domain.LoginResult, error)
}

// OTPPrompter asks for a two-factor code; attempt counts the codes already
// rejected for the same challenge.
type OTPPrompter interface {
	PromptOTP(attempt int) (string, error)
}

// MFARequiredError is returned when a login needs a two-factor code and no
// source can supply one.
func MFARequiredError() *domain.Error {
	return domain.NewUserError("this account uses two-factor authentication and no code was provided").
		WithHint(LoginHint, "nipa login")
}

func startPasswordLogin(ctx context.Context, executor loginExecutor, host, username, password, otp string) (*domain.LoginResult, *domain.MFAChallenge, error) {
	result, err := executor.LoginWithUsernamePassword(ctx, host, username, password)
	if err != nil {
		return nil, nil, err
	}
	if result.MFAChallenge == "" {
		return result, nil, nil
	}
	challenge := &domain.MFAChallenge{Challenge: result.MFAChallenge, ExpiresIn: result.ExpiresIn}
	if strings.TrimSpace(otp) == "" {
		return nil, challenge, nil
	}
	result, err = completeMFA(ctx, executor, host, challenge.Challenge, otp)
	return result, nil, err
}

func completeMFA(ctx context.Context, executor any, host, challenge, code string) (*domain.LoginResult, error) {
	mfa, ok := executor.(mfaExecutor)
	if !ok {
		return nil, domain.NewUserError("two-factor login is not supported by this client")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, domain.NewUserError("authentication code cannot be empty")
	}
	return mfa.CompleteLoginMFA(ctx, host, challenge, code)
}

// loginWithPassword runs a password login and, when the account asks for a
// second factor, takes the code from otp, then NIPA_OTP, then the prompter.
func loginWithPassword(ctx context.Context, executor loginExecutor, input any, getenv func(string) string, host, username, password, otp string) (*domain.LoginResult, error) {
	if strings.TrimSpace(otp) == "" && getenv != nil {
		otp = getenv(EnvOTP)
	}
	result, challenge, err := startPasswordLogin(ctx, executor, host, username, password, otp)
	if err != nil || challenge == nil {
		return result, err
	}
	prompter, ok := input.(OTPPrompter)
	if !ok {
		return nil, MFARequiredError()
	}
	return promptMFA(ctx, executor, prompter, host, challenge.Challenge)
}

func promptMFA(ctx context.Context, executor loginExecutor, prompter OTPPrompter, host, challenge string) (*domain.LoginResult, error) {
	if _, ok := executor.(mfaExecutor); !ok {
		return nil, domain.NewUserError("two-factor login is not supported by this client")
	}
	var lastErr error
	for attempt := range maxOTPPrompts {
		code, err := prompter.PromptOTP(attempt)
		if err != nil {
			return nil, err
		}
		result, err := completeMFA(ctx, executor, host, challenge, code)
		if err == nil {
			return result, nil
		}
		var domErr *domain.Error
		if !errors.As(err, &domErr) || domErr.Code != 400 {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// LoginWithPassword logs in with a password and, when the account asks for it,
// a two-factor code from otp, NIPA_OTP or prompter (the Auth input when nil).
func (l *Auth) LoginWithPassword(ctx context.Context, address, username, password, otp string, prompter OTPPrompter) error {
	if username == "" {
		return domain.NewUserError("username cannot be empty")
	}
	var input any = l.userInput
	if prompter != nil {
		input = prompter
	}
	host := domain.HostForAddress(address)
	result, err := loginWithPassword(ctx, l.loginExecutor, input, l.getenv, host, username, password, otp)
	if err != nil {
		return err
	}
	result.Email = username
	return l.secureStorage.SaveToken(result)
}

// StartPasswordLogin logs in without prompting. It returns the challenge when
// the account needs a two-factor code and otp is empty; the caller finishes
// with CompleteLoginMFA.
func (l *Auth) StartPasswordLogin(ctx context.Context, address, username, password, otp string) (*domain.MFAChallenge, error) {
	if username == "" {
		return nil, domain.NewUserError("username cannot be empty")
	}
	host := domain.HostForAddress(address)
	result, challenge, err := startPasswordLogin(ctx, l.loginExecutor, host, username, password, otp)
	if err != nil || challenge != nil {
		return challenge, err
	}
	result.Email = username
	return nil, l.secureStorage.SaveToken(result)
}

// CompleteLoginMFA finishes a password login challenge and stores the session.
func (l *Auth) CompleteLoginMFA(ctx context.Context, address, username, challenge, code string) error {
	if strings.TrimSpace(challenge) == "" {
		return domain.NewUserError("two-factor challenge cannot be empty")
	}
	host := domain.HostForAddress(address)
	result, err := completeMFA(ctx, l.loginExecutor, host, challenge, code)
	if err != nil {
		return err
	}
	result.Email = username
	return l.secureStorage.SaveToken(result)
}
