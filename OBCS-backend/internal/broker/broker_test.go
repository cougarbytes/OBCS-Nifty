package broker

import (
	"errors"
	"fmt"
	"testing"
)

func TestRejectionReasonExtractsThroughWrapping(t *testing.T) {
	base := &OrderRejectedError{OrderRef: "241010000012345", Status: "rejected",
		Reason: "17070 : The Price is out of the current execution range"}
	wrapped := fmt.Errorf("order SELL 241010000012345: %w", base)

	reason, ok := RejectionReason(wrapped)
	if !ok {
		t.Fatal("expected rejection to be detected through the wrap chain")
	}
	if reason != base.Reason {
		t.Errorf("reason = %q, want broker verdict", reason)
	}
}

func TestRejectionReasonFallsBackWhenBrokerGivesNoText(t *testing.T) {
	reason, ok := RejectionReason(&OrderRejectedError{Status: "cancelled"})
	if !ok || reason == "" {
		t.Fatalf("want non-empty fallback reason, got %q ok=%v", reason, ok)
	}
}

func TestRejectionReasonIgnoresNonRejections(t *testing.T) {
	if _, ok := RejectionReason(errors.New("dial tcp: i/o timeout")); ok {
		t.Fatal("network error must not classify as a broker rejection")
	}
}
