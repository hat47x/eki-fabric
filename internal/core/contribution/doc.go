// Package contribution builds and validates Contribution Records. A record states
// that a resource provider supplied a measured quantity of compute to one
// accepted result, and it states how strongly that measurement is established.
//
// A record reports what was observed and by whom. It is not a currency, price,
// exchange rate, score, or reputation input, and nothing in this package
// converts it into one. Any mapping from records to value belongs to an
// external policy.
//
// Where the requirements allow more than one behavior, the package chooses the
// more conservative one, as follows.
//
//   - A worker's own report is self-reported. It cannot be combined with any
//     other observation, so the worker cannot borrow another observer's level.
//   - Cross-validation needs two distinct non-worker principals that report the
//     same quantity exactly. Disagreement is an error, not an average.
//   - A zero quantity is rejected. An absent measurement is not a contribution.
//   - Text is bounded in bytes, must be single-line, and oversized input is
//     rejected rather than truncated.
//   - A measurement window lasts at most 24 hours. Longer work yields several
//     records.
//
// The package performs no I/O, reads no clock, and starts no goroutines. It
// trusts the caller on two facts it cannot check itself: that a result was
// accepted, and that an observer's kind, including attested-meter, was
// established by the caller's verification layer.
package contribution
