// Package afspec provides an idiomatic Go implementation of the Python afspec
// library, enabling Go programs to load, validate, mutate, save, and render
// agent-fox specification packages with byte-for-byte round-trip fidelity.
//
// Spec structs are treated as value types — mutation methods such as Transition
// and Supersede return new Spec copies rather than modifying the receiver in
// place, matching the Python immutable Pydantic model pattern.
//
// # Concurrency
//
// This package provides no goroutine-safety guarantees. Spec instances are not
// safe for concurrent use by multiple goroutines. Callers are responsible for
// synchronizing concurrent access to Spec instances externally (e.g. using a
// sync.Mutex or by confining each Spec to a single goroutine).
package afspec
