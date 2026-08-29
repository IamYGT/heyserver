package security

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpIssuer    = "HServer Panel"
	totpPeriod    = 30
	totpDigits    = otp.DigitsSix
	totpAlgorithm = otp.AlgorithmSHA1
	recoveryCount = 8
)

// TOTPSetup holds the result of a new TOTP secret generation.
type TOTPSetup struct {
	Secret        string   `json:"secret"`
	QRCodePNG     []byte   `json:"-"`
	OTPAuthURL    string   `json:"otpAuthUrl"`
	RecoveryCodes []string `json:"recoveryCodes"`
}

// GenerateTOTP creates a new TOTP key for the given account email.
func GenerateTOTP(email string) (*TOTPSetup, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: email,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   totpAlgorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("totp generate: %w", err)
	}

	img, err := key.Image(200, 200)
	if err != nil {
		return nil, fmt.Errorf("totp qr image: %w", err)
	}

	var buf bytes.Buffer
	if err = png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("totp qr encode: %w", err)
	}

	recoveryCodes, err := generateRecoveryCodes(recoveryCount)
	if err != nil {
		return nil, fmt.Errorf("recovery codes: %w", err)
	}

	return &TOTPSetup{
		Secret:        key.Secret(),
		QRCodePNG:     buf.Bytes(),
		OTPAuthURL:    key.URL(),
		RecoveryCodes: recoveryCodes,
	}, nil
}

// VerifyTOTP validates a 6-digit code against the stored secret.
func VerifyTOTP(secret, code string) (bool, error) {
	valid, err := totp.ValidateCustom(
		code, secret, time.Now().UTC(),
		totp.ValidateOpts{
			Period:    totpPeriod,
			Skew:      1,
			Digits:    totpDigits,
			Algorithm: totpAlgorithm,
		},
	)
	if err != nil {
		return false, fmt.Errorf("totp validate: %w", err)
	}
	return valid, nil
}

func generateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, n)
	for i := range codes {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		h := strings.ToUpper(hex.EncodeToString(raw))
		codes[i] = h[:5] + "-" + h[5:10]
	}
	return codes, nil
}
