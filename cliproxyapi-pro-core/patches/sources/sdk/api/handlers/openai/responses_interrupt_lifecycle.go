package openai

import (
	"github.com/tidwall/gjson"
	"strings"
)

// observe updates interrupt eligibility before exposing an event downstream.
// State is confined to one client socket, including its duplex responses.
func (s *responsesLocalInterrupt) observe(payload []byte) {
	if s == nil {
		return
	}
	responseID := gjson.GetBytes(payload, "response.id")
	id := responseID.String()
	if responseID.Type != gjson.String || strings.TrimSpace(id) == "" {
		return
	}
	event := gjson.GetBytes(payload, "type").String()
	s.mu.Lock()
	defer s.mu.Unlock()
	if event == "response.created" {
		if s.activeIDs == nil {
			s.activeIDs = make(map[string]struct{})
		}
		s.activeIDs[id] = struct{}{}
	} else if isResponsesWebsocketCompletionEvent(event) || event == "response.incomplete" || event == "response.failed" || event == "response.cancelled" {
		delete(s.activeIDs, id)
		if s.terminalIDs == nil {
			s.terminalIDs = make(map[string]struct{})
		}
		s.terminalIDs[id] = struct{}{}
	}
}

func (s *responsesLocalInterrupt) classify(payload []byte) (active, terminal bool) {
	if s == nil {
		return false, false
	}
	responseID := gjson.GetBytes(payload, "response_id")
	if responseID.Type != gjson.String || strings.TrimSpace(responseID.String()) == "" {
		return false, false
	}
	id := responseID.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, active = s.activeIDs[id]
	_, terminal = s.terminalIDs[id]
	return s.active && active && !terminal, terminal
}

// completeInterrupt records the locally generated terminal response before its
// acknowledgement becomes visible to the client.
func (s *responsesLocalInterrupt) completeInterrupt(payload []byte) {
	if s == nil {
		return
	}
	id := gjson.GetBytes(payload, "response_id").String()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activeIDs, id)
	if s.terminalIDs == nil {
		s.terminalIDs = make(map[string]struct{})
	}
	s.terminalIDs[id] = struct{}{}
}
