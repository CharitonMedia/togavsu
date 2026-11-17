package marketplace

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// License key encoding (CORE-CHANGES-2 §15.5 and §17.3)
//
// A license key is one string:
//
//	togavsu-license-v1.<payload_b64url>.<sig_b64url>
//
// payload_b64url is the unpadded Raw URL-safe base64 encoding of this exact UTF-8
// statement (LF line endings, no trailing spaces, one trailing newline after the
// last field):
//
//	togavsu-license-v1
//	install_id=<install id>
//	module=<module id>
//	paid_through=<YYYY-MM-DD>
//
// sig_b64url is the unpadded Raw URL-safe base64 encoding of the Ed25519 signature
// of those exact statement bytes, made with the marketplace's license signing key.
//
// The marketplace public key is a PKIX "PUBLIC KEY" PEM block holding Ed25519, the
// same form as package signing keys (internal/marketplace.PublicKeyPEM). Modules and
// core-update-2c must check keys with CheckLicense against that PEM. GET
// /v1/license-public-key returns it.
//
// paid_through is a UTC calendar date. CheckLicense accepts a key when the given
// instant's UTC date is on or before paid_through. Grace periods are the module's
// concern; this check does not add any.

const (
	licensePrefix   = "togavsu-license-v1"
	licenseKeyParts = 3
)

// ErrLicenseInvalid means the key is malformed, the signature fails, the install or
// module does not match, or the paid-through date has passed.
var ErrLicenseInvalid = errors.New("license_invalid")

// LicenseStatement is the parsed payload of a key.
type LicenseStatement struct {
	InstallID   string
	ModuleID    string
	PaidThrough time.Time // UTC midnight of the paid-through date
	Payload     []byte
}

// FormatLicenseStatement builds the exact bytes that are signed.
func FormatLicenseStatement(installID, moduleID string, paidThrough time.Time) []byte {
	day := paidThrough.UTC().Format("2006-01-02")
	return []byte(fmt.Sprintf("%s\ninstall_id=%s\nmodule=%s\npaid_through=%s\n",
		licensePrefix, installID, moduleID, day))
}

// IssueLicense signs a license key for installID and moduleID through paidThrough's date.
func IssueLicense(priv ed25519.PrivateKey, installID, moduleID string, paidThrough time.Time) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("marketplace: license signing key is missing")
	}
	installID = strings.TrimSpace(installID)
	moduleID = strings.TrimSpace(moduleID)
	if installID == "" || moduleID == "" {
		return "", invalid("install_id", "install_id and module are required")
	}
	payload := FormatLicenseStatement(installID, moduleID, paidThrough)
	sig := ed25519.Sign(priv, payload)
	return licensePrefix + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// ParseLicenseKey splits and verifies the signature, returning the statement.
func ParseLicenseKey(key string, pub ed25519.PublicKey) (LicenseStatement, error) {
	parts := strings.Split(key, ".")
	if len(parts) != licenseKeyParts || parts[0] != licensePrefix {
		return LicenseStatement{}, ErrLicenseInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return LicenseStatement{}, ErrLicenseInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return LicenseStatement{}, ErrLicenseInvalid
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, payload, sig) {
		return LicenseStatement{}, ErrLicenseInvalid
	}
	st, err := parseLicensePayload(payload)
	if err != nil {
		return LicenseStatement{}, err
	}
	st.Payload = payload
	return st, nil
}

func parseLicensePayload(payload []byte) (LicenseStatement, error) {
	lines := strings.Split(string(payload), "\n")
	// Expect prefix, three fields, and a final empty line from the trailing newline.
	if len(lines) < 5 || lines[0] != licensePrefix || lines[len(lines)-1] != "" {
		return LicenseStatement{}, ErrLicenseInvalid
	}
	var st LicenseStatement
	for _, line := range lines[1 : len(lines)-1] {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return LicenseStatement{}, ErrLicenseInvalid
		}
		switch k {
		case "install_id":
			st.InstallID = v
		case "module":
			st.ModuleID = v
		case "paid_through":
			day, err := time.ParseInLocation("2006-01-02", v, time.UTC)
			if err != nil {
				return LicenseStatement{}, ErrLicenseInvalid
			}
			st.PaidThrough = day
		default:
			return LicenseStatement{}, ErrLicenseInvalid
		}
	}
	if st.InstallID == "" || st.ModuleID == "" || st.PaidThrough.IsZero() {
		return LicenseStatement{}, ErrLicenseInvalid
	}
	return st, nil
}

// CheckLicense verifies key offline for wantInstallID and wantModuleID at now.
// core-update-2c and mo-estate-planning must use this exact check (or a byte-identical
// reimplementation against the same encoding above).
func CheckLicense(key string, pub ed25519.PublicKey, wantInstallID, wantModuleID string, now time.Time) error {
	st, err := ParseLicenseKey(key, pub)
	if err != nil {
		return err
	}
	if st.InstallID != wantInstallID || st.ModuleID != wantModuleID {
		return ErrLicenseInvalid
	}
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if day.After(st.PaidThrough) {
		return ErrLicenseInvalid
	}
	return nil
}

// RenewProofMessage is what an install signs to fetch the current license key.
func RenewProofMessage(installID, moduleID string) []byte {
	return []byte(fmt.Sprintf("togavsu-license-renew-v1\ninstall_id=%s\nmodule=%s\n", installID, moduleID))
}

// CancelProofMessage is what an install signs to cancel its own subscription.
func CancelProofMessage(installID, moduleID string) []byte {
	return []byte(fmt.Sprintf("togavsu-license-cancel-v1\ninstall_id=%s\nmodule=%s\n", installID, moduleID))
}
