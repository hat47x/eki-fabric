package admission

import "testing"

func TestTheDeclaredClassesAreAccepted(t *testing.T) {
	for _, value := range append([]string{""}, DataClasses...) {
		if !ValidDataClass(value) {
			t.Fatalf("ValidDataClass(%q) = false", value)
		}
	}
}

func TestAClassOutsideTheVocabularyIsRefused(t *testing.T) {
	for _, value := range []string{"secret", "PUBLIC", "internal ", "unclassified"} {
		if ValidDataClass(value) {
			t.Fatalf("ValidDataClass(%q) = true: a class nobody defined would look like one", value)
		}
	}
}
