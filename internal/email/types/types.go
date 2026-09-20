package types

// EmailType represents the type of email to send
type EmailType string

const (
	EmailTypeInvitation      EmailType = "invitation"
	EmailTypeWelcome         EmailType = "welcome"
	EmailTypePaymentSuccess  EmailType = "payment_success"
	EmailTypePaymentFailed   EmailType = "payment_failed"
	EmailTypeUsageWarning    EmailType = "usage_warning"
	EmailTypeUsageExceeded   EmailType = "usage_exceeded"
	EmailTypeActivationToken    EmailType = "activation_token"
	EmailTypeMagicLink          EmailType = "magic_link"
	EmailTypePasswordReset      EmailType = "password_reset"
	EmailTypeEmailVerification  EmailType = "email_verification"
)


