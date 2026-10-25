package web

import "testing"

func TestDict(t *testing.T) {
	t.Parallel()
	m, err := Dict("a", 1, "b", "two")
	if err != nil || m["a"] != 1 || m["b"] != "two" {
		t.Fatalf("Dict = %v, %v", m, err)
	}
	if _, err := Dict("a"); err == nil {
		t.Error("odd arguments accepted")
	}
	if _, err := Dict(1, 2); err == nil {
		t.Error("non-string key accepted")
	}
}
