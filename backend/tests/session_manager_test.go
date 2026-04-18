package tests

import (
	"testing"

	"bridgewithclawandfreeswitch/backend/internal/session"
)

func TestSessionManagerCreateAndClose(t *testing.T) {
	mgr := session.NewManager()
	s := mgr.Create("call-123")
	if s.CallID != "call-123" {
		t.Fatalf("expected call id call-123, got %s", s.CallID)
	}

	mgr.Close("call-123")
	if _, ok := mgr.Get("call-123"); ok {
		t.Fatal("expected session to be removed")
	}
}

func TestSessionManagerUpdateBySessionID(t *testing.T) {
	mgr := session.NewManager()
	s := mgr.Create("call-456")

	updated, err := mgr.Update(s.ID, func(current *session.Session) error {
		current.State = session.StateListening
		current.Caller = "+8613800138000"
		return nil
	})
	if err != nil {
		t.Fatalf("update returned error: %v", err)
	}

	if updated.State != session.StateListening {
		t.Fatalf("expected state %s, got %s", session.StateListening, updated.State)
	}
	if updated.Caller != "+8613800138000" {
		t.Fatalf("expected caller to be updated, got %s", updated.Caller)
	}
}
