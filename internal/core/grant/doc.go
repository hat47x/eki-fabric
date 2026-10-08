// Package grant models a Compute Grant: a bounded offer by a resource provider
// to let one beneficiary use part of its compute for a defined period, under
// explicit per-class ceilings. The provider can stop the offer later.
//
// A grant is a permission record, not money. It limits what a beneficiary may
// use and lets the provider revoke that use. It gives the beneficiary no
// position inside the provider's network, no access to the provider's data,
// and no claim on results already produced under it. No type in this package
// has a field, method, or constant that assigns a currency, price, credit, or
// exchange rate to a grant.
//
// Where the requirements allow more than one behavior, the package chooses the
// more conservative one, as follows.
//
//   - Delegation is not supported. Issue accepts only the provider the grant
//     names as issuer, so a beneficiary cannot issue a child grant. To narrow a
//     grant, the provider revokes it and issues a new grant with lower ceilings.
//   - Revocation is final and does not move. A second revocation is refused. A
//     revocation never changes the grant's fields or its digest, and a query for
//     an earlier time still answers whether the grant was active then.
//   - A zero MaxConcurrentLeases means that no lease-count cap was set. The
//     percent ceilings still apply, and each active lease holds at least one
//     percent, so one class never has more than 100 concurrent leases.
//   - A zero Grant value is a nil grant and is always refused by Admit and
//     Revoke.
//   - Admission uses the caller's counts of active leases. The package keeps no
//     lease state, so those counts must be current.
//
// The package checks the shape of identities and the authority of the issuer
// and revoker against the grant's named provider. It does not check that the
// named provider really owns the compute. The caller must establish that, for
// example through the trust and policy authority.
//
// The package performs no I/O, reads no clock, and starts no goroutines. Time
// is always an input.
package grant
