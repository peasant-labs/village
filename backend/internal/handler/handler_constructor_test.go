package handler

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// Compile-time contract: composition accepts exactly one transcript store.
// A variadic constructor or alternate return shape cannot satisfy this type.
var _ func(*config.Config, *pgxpool.Pool, storage.TranscriptBlobStore) *Handler = New
