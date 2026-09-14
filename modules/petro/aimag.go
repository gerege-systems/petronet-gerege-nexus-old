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

// normalizeAimag trims a submitted province and says whether it is one.
// Empty is allowed here; the callers that require a province say so themselves.
func normalizeAimag(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true
	}
	for _, known := range Aimags {
		if name == known {
			return name, true
		}
	}
	return name, false
}
