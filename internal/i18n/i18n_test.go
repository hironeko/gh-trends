package i18n

import "testing"

func TestDefaultLanguageIsJapanese(t *testing.T) {
	if got := Lang(); got != "jp" {
		t.Fatalf("Lang() = %q, want %q", got, "jp")
	}
}
