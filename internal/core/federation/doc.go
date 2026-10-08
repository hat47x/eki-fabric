// Package federation holds the trust binding for federation between trust
// domains: separate organizations or administrative boundaries that each keep
// their own identity authority and cooperate in a limited way.
//
// A trust domain name is bound to exactly one trust bundle at a time, and an
// identity that claims a domain is verified only against the bundle bound to
// that domain. Bundles of different domains are never merged into one pool, and
// no function returns a union of them. A federation link lists the purposes it
// allows, so federation never means blanket trust.
//
// Where the rules leave a choice open, the package fails closed: a non-canonical
// domain name is rejected rather than normalized, and an unbound domain, an
// unknown key, a stale or future-dated bundle, or a revoked domain is a rejection
// with a stable Reason. A revoked domain stays in the Registry and cannot be
// federated again there.
//
// Time is always an argument. The package reads no clock, performs no I/O, and
// starts no goroutines. The Registry is safe for concurrent use. The package
// does not authenticate the callers of its mutating methods, and it takes the
// validated-at time of a bundle from the caller, so both must come from trusted
// paths.
package federation
