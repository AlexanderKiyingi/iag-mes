package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"iag-mes/backend/internal/events"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrBadInput  = errors.New("bad input")
)

type Store struct {
	pool *pgxpool.Pool
	bus  *events.Bus
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) SetEventBus(bus *events.Bus) {
	s.bus = bus
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}
