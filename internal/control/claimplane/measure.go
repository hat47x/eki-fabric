package claimplane

// SetRefusalHook connects ADR-0039's counter for refusals that this boundary made
// itself: the wrong Worker, a malformed claim, or a missing identity. A claim that
// claimaccept rejects is counted by that layer, not here, so the two numbers do
// not describe the same event twice.
func (s *Service) SetRefusalHook(f func(reason string)) { s.onRefusal = f }

func (s *Service) recordRefusal(reason string) {
	if s != nil && s.onRefusal != nil {
		s.onRefusal(reason)
	}
}
