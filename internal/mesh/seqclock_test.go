package mesh

import "testing"

func TestSeqClockStrictlyIncreasing(t *testing.T) {
	t.Parallel()
	c := &SeqClock{}
	prev := int64(0)
	for i := 0; i < 100; i++ { // tight loop → same wall-clock second
		got := c.Next().Unix()
		if got <= prev {
			t.Fatalf("call %d: got %d, not greater than previous %d", i, got, prev)
		}
		prev = got
	}
}
