package auth

import (
	"bytes"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}

	key := DeriveKey("test-passphrase", salt)
	plaintext := []byte("secret data")

	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(plaintext, ciphertext) {
		t.Error("ciphertext should differ from plaintext")
	}

	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	salt, _ := GenerateSalt()
	key1 := DeriveKey("passphrase1", salt)
	key2 := DeriveKey("passphrase2", salt)

	ciphertext, _ := Encrypt([]byte("secret"), key1)
	_, err := Decrypt(ciphertext, key2)
	if err == nil {
		t.Error("expected error with wrong key")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	_, err := Decrypt([]byte("short"), key)
	if err == nil {
		t.Error("expected error for short ciphertext")
	}
}

func TestVerifyPassphrase(t *testing.T) {
	salt, _ := GenerateSalt()
	passphrase := "my-secure-passphrase"

	hash := HashPassphrase(passphrase, salt)

	if !VerifyPassphrase(passphrase, salt, hash) {
		t.Error("valid passphrase should verify")
	}

	if VerifyPassphrase("wrong-passphrase", salt, hash) {
		t.Error("wrong passphrase should not verify")
	}
}

func TestGenerateSalt(t *testing.T) {
	salt1, _ := GenerateSalt()
	salt2, _ := GenerateSalt()

	if bytes.Equal(salt1, salt2) {
		t.Error("salts should be unique")
	}

	if len(salt1) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt1))
	}
}
