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
		{"Дорноговь аймаг", "Дорноговь аймаг", false},
		{"дорноговь", "дорноговь", false},
	} {
		got, ok := normalizeAimag(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normalizeAimag(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
