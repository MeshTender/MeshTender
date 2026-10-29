//go:build browser

package e2e

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestStepNamesAHang pins what the step helpers are for: a step that never
// finishes fails at its own limit, not the test's, and the error says which
// step it was. Needs no browser — the actions are plain functions.
func TestStepNamesAHang(t *testing.T) {
	t.Parallel()
	hang := chromedp.ActionFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	start := time.Now()
	err := stepWithin("wait for the broker card", 50*time.Millisecond, hang).Do(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline", err)
	}
	if !strings.Contains(err.Error(), `"wait for the broker card"`) {
		t.Errorf("err = %q, want the step's name", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("hung for %v; the step's limit is 50ms", took)
	}
}

// TestStepRunsItsActionsInOrder: a step is its actions, in order, stopping at
// the first failure — which it reports under its name.
func TestStepRunsItsActionsInOrder(t *testing.T) {
	t.Parallel()
	var ran []string
	act := func(name string, err error) chromedp.Action {
		return chromedp.ActionFunc(func(context.Context) error {
			ran = append(ran, name)
			return err
		})
	}
	if err := step("ok", act("a", nil), act("b", nil)).Do(t.Context()); err != nil || strings.Join(ran, ",") != "a,b" {
		t.Fatalf("ran %v, err %v", ran, err)
	}
	ran = nil
	boom := errors.New("boom")
	err := step("click save", act("a", boom), act("b", nil)).Do(t.Context())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), `"click save"`) || strings.Join(ran, ",") != "a" {
		t.Fatalf("ran %v, err %v", ran, err)
	}
}
