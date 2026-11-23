package binarystore_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func signRaw(t *testing.T, key *rsa.PrivateKey, s string) string {
	t.Helper()
	h := sha256.Sum256([]byte(s))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func rsaSmall() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 1024) }
