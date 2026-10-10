package openai

import "testing"

// Failure cases: unknown IDs must not reach upstream; terminal IDs must be
// harmless across turns; queued old interrupts must not cancel a new response;
// disconnect must reject active delivery. Duplex responses share one forwarder.
func TestResponsesInterruptLifecycle(t *testing.T) {
	s := newResponsesLocalInterrupt()
	s.begin()
	created := func(id string) { s.observe([]byte(`{"type":"response.created","response":{"id":"` + id + `"}}`)) }
	done := func(id string) { s.observe([]byte(`{"type":"response.completed","response":{"id":"` + id + `"}}`)) }
	payload := func(id string) []byte { return []byte(`{"type":"response.interrupt","response_id":"` + id + `"}`) }
	created("r1")
	if active, terminal := s.classify(payload("unknown")); active || terminal {
		t.Fatal("unknown accepted")
	}
	if !s.deliver(payload("r1")) {
		t.Fatal("active interrupt rejected")
	}
	queued := <-s.framesChan()
	done("r1")
	if active, terminal := s.classify(payload("r1")); active || !terminal {
		t.Fatal("terminal ID not retained")
	}
	created("r2")
	if s.deliver(queued) {
		t.Fatal("stale interrupt accepted by next duplex response")
	}
	if active, terminal := s.classify(payload("r2")); !active || terminal {
		t.Fatal("new duplex response not active")
	}
	created("parallel")
	if active, _ := s.classify(payload("r2")); !active {
		t.Fatal("parallel response replaced existing active ID")
	}
	done("parallel")
	if active, _ := s.classify(payload("r2")); !active {
		t.Fatal("parallel completion cleared another response")
	}
	s.end()
	if s.deliver(payload("r2")) {
		t.Fatal("disconnected response accepted")
	}
	s.begin()
	created("r-http")
	if active, terminal := s.classify(payload("r1")); active || !terminal {
		t.Fatal("old websocket terminal ID lost across HTTP switch")
	}
	if !s.deliver(payload("r-http")) {
		t.Fatal("HTTP fallback interrupt rejected")
	}
	// Local incomplete and explicit response failures also terminate IDs.
	s.completeInterrupt(payload("r-http"))
	if active, terminal := s.classify(payload("r-http")); active || !terminal {
		t.Fatal("local interrupt did not terminate ID")
	}
	for _, event := range []string{"response.failed", "response.cancelled"} {
		created(event)
		s.observe([]byte(`{"type":"` + event + `","response":{"id":"` + event + `"}}`))
		if active, terminal := s.classify(payload(event)); active || !terminal {
			t.Fatal("failed response stayed active")
		}
	}
}
