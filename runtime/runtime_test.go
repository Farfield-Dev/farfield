package runtime

import "testing"

func TestTransitions(t *testing.T) {
	t.Parallel()
	if err := ValidateTransition(StatusQueued, StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(StatusCompleted, StatusRunning); err == nil {
		t.Fatal("terminal run was allowed to restart")
	}
	if !StatusAmbiguous.Terminal() || StatusWaiting.Terminal() {
		t.Fatal("terminal status classification is incorrect")
	}
}
