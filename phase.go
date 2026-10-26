package doger

import "fmt"

// ContainerPhase is where a root container is in its lifecycle. Its zero
// value is PhaseIdle.
type ContainerPhase int

const (
	// PhaseIdle is a container that has not started nor shut down.
	PhaseIdle ContainerPhase = iota
	// PhaseStarting is a container whose first Start is running.
	PhaseStarting
	// PhaseStarted is a container whose Start succeeded, while its Started
	// hooks run.
	PhaseStarted
	// PhaseReady is a container whose Start succeeded and whose Started
	// hooks have returned.
	PhaseReady
	// PhaseFailed is a container whose first Start failed.
	PhaseFailed
	// PhaseClosing is a container whose root Shutdown is running.
	PhaseClosing
	// PhaseClosed is a container whose root Shutdown completed.
	PhaseClosed
)

// String returns the lower-case phase name, or "ContainerPhase(n)" for an
// unknown value.
func (p ContainerPhase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseStarting:
		return "starting"
	case PhaseStarted:
		return "started"
	case PhaseReady:
		return "ready"
	case PhaseFailed:
		return "failed"
	case PhaseClosing:
		return "closing"
	case PhaseClosed:
		return "closed"
	default:
		return fmt.Sprintf("ContainerPhase(%d)", int(p))
	}
}

// Phase returns the container's current phase. Once root Shutdown began,
// the close phase is reported, whatever the start phase. A clone starts
// idle; child scopes have no phase of their own.
func (s *RootScope) Phase() ContainerPhase {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	switch s.closePhase {
	case closeClosing:
		return PhaseClosing
	case closeClosed:
		return PhaseClosed
	case closeOpen:
	}

	switch s.startPhase {
	case startStarting:
		return PhaseStarting
	case startStarted:
		return PhaseStarted
	case startReady:
		return PhaseReady
	case startFailed:
		return PhaseFailed
	case startIdle:
	}

	return PhaseIdle
}
