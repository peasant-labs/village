package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type encryptedReadResult struct {
	Plaintext   []byte
	Row         sqlc.Transcript
	Identity    storage.ContentIdentity
	NotModified bool
}

func (h *Handler) readEncryptedTranscript(ctx context.Context, initial sqlc.Transcript, ifNoneMatch string, authorize func(sqlc.Transcript) bool) (encryptedReadResult, error) {
	if h.blobs == nil {
		return encryptedReadResult{}, errors.New("encrypted transcript read failed because the blob store was not composed in handler.readEncryptedTranscript before body access; no response body was started; configure key custody and object storage, then restart and retry")
	}
	read := func(row sqlc.Transcript) (encryptedReadResult, error) {
		descriptor, err := descriptorFromTranscript(row)
		if err != nil {
			return encryptedReadResult{}, err
		}
		loaded, err := identityFromTranscript(row)
		if err != nil {
			return encryptedReadResult{}, err
		}
		if known, ok := loaded.Known(); ok && ifNoneMatchMatches(ifNoneMatch, string(known.Hash())) {
			return encryptedReadResult{Row: row, Identity: known, NotModified: true}, nil
		}
		plaintext, identity, err := h.blobs.Read(ctx, uuidFromPg(row.ID), descriptor, loaded)
		if err != nil {
			return encryptedReadResult{}, err
		}
		return encryptedReadResult{Plaintext: plaintext, Row: row, Identity: identity}, nil
	}
	result, err := read(initial)
	if err == nil || !errors.Is(err, storage.ErrObjectNotFound) {
		return result, err
	}
	fresh, reloadErr := h.queries.GetTranscriptByID(ctx, initial.ID)
	if reloadErr != nil || !authorize(fresh) {
		return encryptedReadResult{}, fmt.Errorf("encrypted transcript reload was denied or missing after an immutable generation disappeared in handler.readEncryptedTranscript during the sole authorized retry; no body was returned; refresh access or ask the owner to republish")
	}
	oldDescriptor, oldErr := descriptorFromTranscript(initial)
	newDescriptor, newErr := descriptorFromTranscript(fresh)
	if oldErr != nil || newErr != nil {
		return encryptedReadResult{}, errors.Join(oldErr, newErr)
	}
	if descriptorsEqual(oldDescriptor, newDescriptor) {
		return encryptedReadResult{}, fmt.Errorf("encrypted transcript read failed because the unchanged immutable generation is absent in handler.readEncryptedTranscript after the sole reload; no body was returned and repeated retries are unsafe; reconcile object storage and the database descriptor: %w", err)
	}
	return read(fresh)
}

func descriptorsEqual(a, b storage.BlobDescriptor) bool {
	return a.ObjectKey() == b.ObjectKey() && a.Algorithm() == b.Algorithm() && a.KeyVersion() == b.KeyVersion() && string(a.WrappedDEK()) == string(b.WrappedDEK())
}
