//go:build integration

package handler

import (
	"errors"

	"github.com/aws/smithy-go"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// rustfsTestKeyAbsent checks the actual test provider response, rather than
// treating any uncertain outage as proof that cleanup removed an object. It
// makes no claim about the separately deployed provider.
func rustfsTestKeyAbsent(err error) bool {
	var api smithy.APIError
	return errors.Is(err, storage.ErrObjectLookupUncertain) && errors.As(err, &api) && api.ErrorCode() == "NoSuchKey"
}
