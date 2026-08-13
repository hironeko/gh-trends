package animation

import "testing"

func TestSuppressSpinnersPreventsStart(t *testing.T) {
	restore := SuppressSpinners()
	spinner := NewShibaSpinner("test", false)
	spinner.Start()

	globalSpinnerMutex.Lock()
	active := activeSpinner
	globalSpinnerMutex.Unlock()
	if active != nil {
		t.Fatal("spinner became active while suppression was enabled")
	}

	restore()
	globalSpinnerMutex.Lock()
	suppressed := spinnersSuppressed
	globalSpinnerMutex.Unlock()
	if suppressed {
		t.Fatal("spinner suppression was not restored")
	}
}
