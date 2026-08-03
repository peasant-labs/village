package config

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

type KeyVersion int32

type TranscriptKeyring struct {
	active KeyVersion
	keys   map[KeyVersion][]byte
}

func ParseTranscriptKeyring(activeText, keyringJSON string) (*TranscriptKeyring, error) {
	active64, err := strconv.ParseInt(activeText, 10, 32)
	if err != nil || active64 <= 0 {
		return nil, fmt.Errorf("transcript KEK configuration failed because TRANSCRIPT_KEK_ACTIVE_VERSION is not a positive 32-bit integer in config.ParseTranscriptKeyring during authority loading; encrypted transcript operations cannot start; correct the named variable to a positive version present in TRANSCRIPT_KEK_KEYRING")
	}
	dec := json.NewDecoder(bytes.NewBufferString(keyringJSON))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, keyringError("TRANSCRIPT_KEK_KEYRING is not a JSON object")
	}
	keys := make(map[KeyVersion][]byte)
	for dec.More() {
		nameToken, err := dec.Token()
		if err != nil {
			return nil, keyringError("a key version could not be decoded")
		}
		name := nameToken.(string)
		v64, err := strconv.ParseInt(name, 10, 32)
		if err != nil || v64 <= 0 {
			return nil, keyringError("a key version is not a positive 32-bit integer")
		}
		v := KeyVersion(v64)
		if _, exists := keys[v]; exists {
			return nil, keyringError("a duplicate key version is present")
		}
		var encoded string
		if err := dec.Decode(&encoded); err != nil {
			return nil, keyringError("a key value is not a string")
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, keyringError("a key is not strict base64 encoding of exactly 32 bytes")
		}
		keys[v] = key
	}
	if _, err := dec.Token(); err != nil {
		return nil, keyringError("the keyring object is incomplete")
	}
	if tok, err := dec.Token(); err != io.EOF || tok != nil {
		return nil, keyringError("trailing JSON content is present")
	}
	active := KeyVersion(active64)
	if _, ok := keys[active]; !ok {
		return nil, keyringError("the active version is absent from the keyring")
	}
	return &TranscriptKeyring{active: active, keys: keys}, nil
}

func keyringError(why string) error {
	return fmt.Errorf("transcript KEK configuration failed because %s in config.ParseTranscriptKeyring during authority loading; encrypted transcript operations cannot start; correct TRANSCRIPT_KEK_KEYRING and ensure the active positive version is present without exposing key values", why)
}

func (k *TranscriptKeyring) Wrap(_ context.Context, plaintext, aad []byte) ([]byte, KeyVersion, error) {
	out, err := seal(k.keys[k.active], plaintext, aad)
	if err != nil {
		return nil, 0, fmt.Errorf("DEK wrapping failed because AES-GCM could not seal the key in config.TranscriptKeyring.Wrap during transcript encryption; no usable descriptor was produced; verify the configured KEK and retry: %w", err)
	}
	return out, k.active, nil
}
func (k *TranscriptKeyring) Unwrap(_ context.Context, wrapped []byte, version KeyVersion, aad []byte) ([]byte, error) {
	key, ok := k.keys[version]
	if !ok {
		return nil, fmt.Errorf("DEK unwrapping failed because key version %d is unavailable in config.TranscriptKeyring.Unwrap during transcript decryption; the affected transcript remains unreadable; restore that version to TRANSCRIPT_KEK_KEYRING and retry", version)
	}
	out, err := open(key, wrapped, aad)
	if err != nil {
		return nil, fmt.Errorf("DEK unwrapping failed because authentication rejected the wrapped key in config.TranscriptKeyring.Unwrap during transcript decryption; the affected transcript remains unreadable; verify descriptor identity and key custody, then restore or escalate: %w", err)
	}
	return out, nil
}
func (k *TranscriptKeyring) Rewrap(ctx context.Context, wrapped []byte, version KeyVersion, aad []byte) ([]byte, KeyVersion, error) {
	plain, err := k.Unwrap(ctx, wrapped, version, aad)
	if err != nil {
		return nil, 0, err
	}
	return k.Wrap(ctx, plain, aad)
}
func (k *TranscriptKeyring) ActiveVersion() KeyVersion { return k.active }

func seal(key, plain, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nil, plain, aad), nil
}
func open(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nil, sealed, aad)
}
