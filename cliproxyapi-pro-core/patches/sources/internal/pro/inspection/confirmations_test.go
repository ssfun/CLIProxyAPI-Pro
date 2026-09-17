package inspection

import "testing"

func TestConfirmationCounterCountsAndClearsPrefix(t *testing.T) {
	counter := NewConfirmationCounter()
	if confirmed, count, _ := counter.Confirm("auth|disable|quota", 2); confirmed || count != 1 {
		t.Fatalf("first confirmation = %v, %d", confirmed, count)
	}
	counter.BeginRun()
	if confirmed, count, _ := counter.Confirm("auth|disable|quota", 2); !confirmed || count != 2 {
		t.Fatalf("second confirmation = %v, %d", confirmed, count)
	}
	counter.ClearPrefix("auth|")
	counter.BeginRun()
	if confirmed, count, _ := counter.Confirm("auth|disable|quota", 2); confirmed || count != 1 {
		t.Fatalf("cleared confirmation = %v, %d", confirmed, count)
	}
}

func TestConfirmationCounterRequiresConsecutiveRunsAndRestores(t *testing.T) {
	counter := NewConfirmationCounter()
	if confirmed, count, _ := counter.Confirm("auth|delete|invalid", 3); confirmed || count != 1 {
		t.Fatalf("first confirmation = %v, %d", confirmed, count)
	}
	if confirmed, count, _ := counter.Confirm("auth|delete|invalid", 3); confirmed || count != 1 {
		t.Fatalf("duplicate confirmation in one run = %v, %d", confirmed, count)
	}

	restored := NewConfirmationCounter()
	restored.Restore(counter.State())
	restored.BeginRun()
	if confirmed, count, _ := restored.Confirm("auth|delete|invalid", 3); confirmed || count != 2 {
		t.Fatalf("restored second run = %v, %d", confirmed, count)
	}
	restored.BeginRun()
	if confirmed, count, _ := restored.Confirm("other|delete|invalid", 3); confirmed || count != 1 {
		t.Fatalf("unrelated confirmation = %v, %d", confirmed, count)
	}
	restored.BeginRun()
	if confirmed, count, _ := restored.Confirm("auth|delete|invalid", 3); confirmed || count != 1 {
		t.Fatalf("non-consecutive confirmation = %v, %d", confirmed, count)
	}
}

func TestConfirmationCounterResetClearsPortableState(t *testing.T) {
	counter := NewConfirmationCounter()
	counter.Confirm("auth|delete|invalid", 3)
	counter.Reset()
	counter.BeginRun()
	if confirmed, count, _ := counter.Confirm("auth|delete|invalid", 3); confirmed || count != 1 {
		t.Fatalf("confirmation after reset = %v, %d", confirmed, count)
	}
}

func TestConfirmationCounterFollowsNormalCredentialRefreshChain(t *testing.T) {
	counter := NewConfirmationCounter()
	if confirmed, count, _ := counter.ConfirmWithFingerprint("auth|delete|invalid", "epoch-1:gen-1:token-a", "epoch-1:gen-2:token-b", 2); confirmed || count != 1 {
		t.Fatalf("first confirmation = %v, %d", confirmed, count)
	}
	counter.BeginRun()
	if confirmed, count, _ := counter.ConfirmWithFingerprint("auth|delete|invalid", "epoch-1:gen-2:token-b", "epoch-1:gen-3:token-c", 2); !confirmed || count != 2 {
		t.Fatalf("refreshed credential confirmation = %v, %d", confirmed, count)
	}
}

func TestConfirmationCounterResetsWhenCredentialChangesBetweenRuns(t *testing.T) {
	counter := NewConfirmationCounter()
	if confirmed, count, _ := counter.ConfirmWithFingerprint("auth|delete|invalid", "epoch-1:gen-1:token-a", "epoch-1:gen-1:token-a", 2); confirmed || count != 1 {
		t.Fatalf("first confirmation = %v, %d", confirmed, count)
	}
	counter.BeginRun()
	if confirmed, count, _ := counter.ConfirmWithFingerprint("auth|delete|invalid", "epoch-2:gen-1:token-b", "epoch-2:gen-1:token-b", 2); confirmed || count != 1 {
		t.Fatalf("replacement credential confirmation = %v, %d, want reset", confirmed, count)
	}
}

func TestConfirmationCounterResetsChangedCredentialWithinRun(t *testing.T) {
	counter := NewConfirmationCounter()
	if confirmed, count, _ := counter.ConfirmWithFingerprint("auth|delete|invalid", "epoch-1:gen-1:token-a", "epoch-1:gen-1:token-a", 2); confirmed || count != 1 {
		t.Fatalf("first confirmation = %v, %d", confirmed, count)
	}
	if confirmed, count, _ := counter.ConfirmWithFingerprint("auth|delete|invalid", "epoch-2:gen-1:token-b", "epoch-2:gen-1:token-b", 2); confirmed || count != 1 {
		t.Fatalf("same-run replacement confirmation = %v, %d, want reset", confirmed, count)
	}
}
