package trustbundle

// Approve records an approval and reports why it was refused. It is the same
// decision as Add, named so a caller that only needs "record this approval" (the
// Operator API, ADR-0038) does not have to know the book's other methods.
func (b *ApprovalBook) Approve(approval Approval) error {
	return b.Add(approval)
}
