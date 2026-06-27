package router

import (
	"errors"
)

var (
	ErrUnknownModel        = errors.New("unknown model — not in allowlist")
	ErrNoEligibleAccount   = errors.New("no eligible account — all exhausted or none available")
	ErrInvalidRouterKey    = errors.New("invalid router key")
)
