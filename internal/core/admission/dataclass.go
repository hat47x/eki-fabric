package admission

// DataClasses is the vocabulary a deployment may use for the data in artifacts
// (ADR-0034 §9). It is a vocabulary, not a judgement: nothing here reads a body,
// and a value says what someone declared, never what was verified.
var DataClasses = []string{"public", "internal", "restricted"}

// ValidDataClass reports whether a declaration uses the vocabulary. Empty is valid
// and means "declared nothing", which is shown as nothing at all (#596).
func ValidDataClass(value string) bool {
	if value == "" {
		return true
	}
	for _, known := range DataClasses {
		if value == known {
			return true
		}
	}
	return false
}
