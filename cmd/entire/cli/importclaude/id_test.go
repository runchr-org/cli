package importclaude

import "testing"

func TestDeriveCheckpointID_StableAndDistinct(t *testing.T) {
	t.Parallel()
	a := DeriveCheckpointID("sess", "turn-1")
	b := DeriveCheckpointID("sess", "turn-1")
	c := DeriveCheckpointID("sess", "turn-2")
	if a != b {
		t.Errorf("not deterministic: %s != %s", a, b)
	}
	if a == c {
		t.Errorf("collision across turns: %s == %s", a, c)
	}
	if a.IsEmpty() {
		t.Error("derived id is empty")
	}
}
