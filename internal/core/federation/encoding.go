package federation

import "encoding/binary"

// appendField appends a 32-bit big-endian length and then the field bytes.
// Every variable-length value in a signed or hashed encoding goes through it,
// so two different field splits cannot produce the same bytes.
func appendField(buf []byte, field []byte) []byte {
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(field)))
	return append(buf, field...)
}

// isASCIIToken reports whether s is 1 to maxLen bytes of printable ASCII with no
// spaces. Key IDs and subjects use this rule, so look-alike characters from
// other scripts cannot make two names look the same.
func isASCIIToken(s string, maxLen int) bool {
	if len(s) == 0 || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
