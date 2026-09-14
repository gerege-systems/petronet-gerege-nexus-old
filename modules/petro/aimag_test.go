package petro

import "testing"

func TestAProvinceIsTakenFromTheList(t *testing.T) {
	if len(Aimags) != 22 {
		t.Fatalf("the list has %d entries, want 21 provinces and the capital", len(Aimags))
	}
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"  Дорноговь ", "Дорноговь", true},
		{"Улаанбаатар", "Улаанбаатар", true},
		{"дорноговь", "Дорноговь", true},
		{"Баян‑Өлгий", "Баян-Өлгий", true},
		{"Дархан уул", "Дархан-Уул", true},
		{"Говь алтай", "Говь-Алтай", true},
		{"Дорноговь аймаг", "Дорноговь аймаг", false},
		{"Gobi", "Gobi", false},
	} {
		got, ok := CanonicalAimag(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CanonicalAimag(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
