package artifactsafety

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// windowsReserved holds the device names Windows refuses as file name
// components, with or without an extension, in upper case. The set is used
// for every platform so that admitted names stay portable.
var windowsReserved = buildWindowsReserved()

func buildWindowsReserved() map[string]bool {
	set := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	// The superscript digits are reserved too, and must be listed explicitly.
	for _, prefix := range []string{"COM", "LPT"} {
		for _, d := range "0123456789¹²³" {
			set[prefix+string(d)] = true
		}
	}
	return set
}

// normalizeName checks a raw entry name and returns its normalized form: the
// name without one trailing slash. A directory's trailing slash is therefore
// dropped. The returned Reason is empty when the name is admitted.
//
// Rules, applied in this order:
//   - the raw name is not empty and is at most maxBytes bytes;
//   - the name is valid UTF-8 with no NUL, no control or format characters;
//   - no backslash, no leading slash, no drive letter, and no character that
//     Windows forbids in file names (:*?"<>|);
//   - no empty, "." or ".." segment, no segment ending in a dot or space, and
//     no Windows device name as a segment;
//   - at most maxDepth segments;
//   - the cleaned path equals the name and stays inside the root.
//
// Names are not case-folded here. Case-insensitive duplicate detection is done
// by the caller.
func normalizeName(raw string, maxBytes, maxDepth int64) (string, Reason) {
	switch {
	case raw == "":
		return "", ReasonNameEmpty
	case int64(len(raw)) > maxBytes:
		return "", ReasonNameTooLong
	case !utf8.ValidString(raw):
		return "", ReasonNameInvalidUTF8
	}
	for _, r := range raw {
		if r == 0 {
			return "", ReasonNameNUL
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", ReasonNameControlChar
		}
	}

	switch {
	case strings.ContainsRune(raw, '\\'):
		return "", ReasonNameBackslash
	case raw[0] == '/':
		return "", ReasonNameAbsolute
	case len(raw) >= 2 && isASCIILetter(raw[0]) && raw[1] == ':':
		return "", ReasonNameDriveLetter
	case strings.ContainsAny(raw, `:*?"<>|`):
		return "", ReasonNameForbiddenChar
	}

	name := strings.TrimSuffix(raw, "/")
	segments := strings.Split(name, "/")
	for _, s := range segments {
		switch {
		case s == "":
			return "", ReasonNameEmptySegment
		case s == ".":
			return "", ReasonNameDotSegment
		case s == "..":
			return "", ReasonNameDotDotSegment
		case strings.HasSuffix(s, ".") || strings.HasSuffix(s, " "):
			return "", ReasonNameWindowsTrailing
		case isWindowsReserved(s):
			return "", ReasonNameWindowsReserved
		}
	}
	if int64(len(segments)) > maxDepth {
		return "", ReasonNameDepth
	}

	// The segment checks above already exclude every way to leave the root.
	// The cleaned-path comparison is kept as a second, independent check.
	cleaned := path.Clean(name)
	if cleaned != name || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ReasonNameOutsideRoot
	}
	return name, ""
}

// isWindowsReserved reports whether a path segment names a Windows device.
// The base name is taken before the first dot, as Windows does.
func isWindowsReserved(segment string) bool {
	base := strings.TrimRight(strings.SplitN(segment, ".", 2)[0], " ")
	return windowsReserved[strings.ToUpper(base)]
}

// ancestorsOf returns the proper ancestor paths of a normalized key, from the
// top level down. For "a/b/c" it returns "a" and "a/b".
func ancestorsOf(key string) []string {
	var out []string
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			out = append(out, key[:i])
		}
	}
	return out
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
