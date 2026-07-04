package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func CertificateFingerprintSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func VerifyPinnedCertificate(rawCerts [][]byte, pinnedSHA256 string) error {
	pinned := normalizeCertificateFingerprint(pinnedSHA256)
	if pinned == "" {
		return fmt.Errorf("pinned certificate fingerprint is required")
	}
	if len(rawCerts) == 0 || len(rawCerts[0]) == 0 {
		return fmt.Errorf("hub server did not present a certificate")
	}
	got := CertificateFingerprintSHA256(rawCerts[0])
	if normalizeCertificateFingerprint(got) != pinned {
		return fmt.Errorf("hub server certificate changed: got SHA256 %s, want %s", got, pinned)
	}
	return nil
}

func normalizeCertificateFingerprint(fingerprint string) string {
	fingerprint = strings.TrimSpace(strings.ToLower(fingerprint))
	fingerprint = strings.TrimPrefix(fingerprint, "sha256:")
	fingerprint = strings.ReplaceAll(fingerprint, ":", "")
	return fingerprint
}
