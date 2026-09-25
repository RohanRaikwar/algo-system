// Package redis provides Redis storage utilities including a circuit breaker.
//
// The CircuitBreaker type is re-exported from the shared circuitbreaker package
// to maintain backward compatibility with existing Redis storage code.
package redis

import (
	"trading-systemv1/internal/circuitbreaker"
)

// Re-export types from the shared circuitbreaker package.
type State = circuitbreaker.State

const (
	StateClosed   = circuitbreaker.StateClosed
	StateOpen     = circuitbreaker.StateOpen
	StateHalfOpen = circuitbreaker.StateHalfOpen
)

// CircuitBreaker is an alias for the shared circuit breaker.
type CircuitBreaker = circuitbreaker.CircuitBreaker

// NewCircuitBreaker creates a circuit breaker (delegates to shared package).
var NewCircuitBreaker = circuitbreaker.NewCircuitBreaker

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = circuitbreaker.ErrCircuitOpen
