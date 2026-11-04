package claimplane

// SetRefusalHook connects ADR-0039's counter for refusals that this boundary made
// itself: the wrong Worker, a malformed claim, a missing identity, or a claim the
// acceptor refused. Counting the last one here keeps "one refusal, one number": the
// acceptor does not count, so nothing is described twice.
func (s *Service) SetRefusalHook(f func(reason string)) { s.onRefusal = f }

func (s *Service) recordRefusal(reason string) {
	if s != nil && s.onRefusal != nil {
		s.onRefusal(reason)
	}
}
