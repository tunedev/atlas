package domain

// StepStatus is where a step's execution stands.
type StepStatus string

const (
	StepStarted StepStatus = "started"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
)

// StepEvent is one change in a step's status, reported as it happens. Err
// is set only when Status is StepFailed.
type StepEvent struct {
	StepID string
	Tool   string
	Status StepStatus
	Err    error
}
