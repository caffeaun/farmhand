package db

import "errors"

// ErrNotFound is returned when a queried entity does not exist.
var ErrNotFound = errors.New("not found")
