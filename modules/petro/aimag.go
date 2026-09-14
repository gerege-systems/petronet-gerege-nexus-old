package petro

import "strings"

// Aimags is the country's first-level divisions, spelled as the register
// spells them, with the capital beside the twenty-one provinces.
//
// A list rather than free text because a province is a key, not a label: the
// province oversight body reads the sites whose aimag equals its own
// (migration 00018), and «Дорноговь» typed by a company beside «Дорноговь
// аймаг» typed by an operator matched nothing — an empty screen with no error.
// frontend/lib/petro/aimags.ts carries the same list for the forms.
var Aimags = []string{
	"Архангай", "Баян-Өлгий", "Баянхонгор", "Булган", "Говь-Алтай", "Говьсүмбэр",
	"Дархан-Уул", "Дорноговь", "Дорнод", "Дундговь", "Завхан", "Орхон",
	"Өвөрхангай", "Өмнөговь", "Сүхбаатар", "Сэлэнгэ", "Төв", "Увс",
	"Ховд", "Хөвсгөл", "Хэнтий", "Улаанбаатар",
}

// aimagMessage answers a name that is not on the list.
const aimagMessage = "аймгийг жагсаалтаас сонгоно уу"

// aimagKey is a name with what people type differently taken out of it: case,
// spaces, and every hyphen a keyboard or a word processor produces
// («Баян‑Өлгий» with U+2011, «Дархан уул» with a space).
func aimagKey(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '-', '‐', '‑', '‒', '–', '—', ' ':
			return -1
		}
		return r
	}, strings.ToLower(name))
}

var aimagByKey = func() map[string]string {
	byKey := make(map[string]string, len(Aimags))
	for _, name := range Aimags {
		byKey[aimagKey(name)] = name
	}
	return byKey
}()

// CanonicalAimag answers the register's spelling of a province and whether it
// is one. Empty is allowed here; the callers that require a province say so
// themselves. An unknown name comes back trimmed so it can be reported.
func CanonicalAimag(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true
	}
	if canonical, ok := aimagByKey[aimagKey(name)]; ok {
		return canonical, true
	}
	return name, false
}

// normalizeAimag is CanonicalAimag under the name the handlers use.
func normalizeAimag(name string) (string, bool) { return CanonicalAimag(name) }
