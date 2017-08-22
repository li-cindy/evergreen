package task

import (
	"encoding/json"

	"github.com/evergreen-ci/evergreen"
	"github.com/mongodb/grip/message"
)

// ResultStatus returns the status for this task that should be displayed in the
// UI. Itb uses a combination of the TaskEndDetails and the Task's status to
// determine the state of the task.
func (t *Task) ResultStatus() string {
	status := t.Status
	if t.Status == evergreen.TaskUndispatched {
		if !t.Activated {
			status = evergreen.TaskInactive
		} else {
			status = evergreen.TaskUnstarted
		}
	} else if t.Status == evergreen.TaskStarted {
		status = evergreen.TaskStarted
	} else if t.Status == evergreen.TaskSucceeded {
		status = evergreen.TaskSucceeded
	} else if t.Status == evergreen.TaskFailed {
		status = evergreen.TaskFailed
		if t.Details.Type == "system" {
			status = evergreen.TaskSystemFailed
			if t.Details.TimedOut {
				if t.Details.Description == "heartbeat" {
					status = evergreen.TaskSystemUnresponse
				} else if t.HasFailedTests() {
					status = evergreen.TaskFailed
				} else {
					status = evergreen.TaskSystemTimedOut
				}
			}
		} else if t.Details.TimedOut {
			status = evergreen.TaskTestTimedOut
		}
	}
	return status
}

// ResultCounts stores a collection of counters related to.
//
// This type implements the grip/message.Composer interface and may be
// passed directly to grip loggers.
type ResultCounts struct {
	Total              int `json:"total"`
	Inactive           int `json:"inactive"`
	Unstarted          int `json:"unstarted"`
	Started            int `json:"started"`
	Succeeded          int `json:"succeeded"`
	Failed             int `json:"failed"`
	SystemFailed       int `json:"system-failed"`
	SystemUnresponsive int `json:"system-unresponsive"`
	SystemTimedOut     int `json:"system-timed-out"`
	TestTimedOut       int `json:"test-timed-out"`

	loggable      bool
	cachedMessage string
	message.Base  `json:"metadata,omitempty"`
}

// GetResultCounts takes a list of tasks and collects their
// outcomes using the same status as the.
func GetResultCounts(tasks []Task) *ResultCounts {
	out := ResultCounts{}

	for _, t := range tasks {
		out.Total++
		switch t.ResultStatus() {
		case evergreen.TaskInactive:
			out.Inactive++
		case evergreen.TaskUnstarted:
			out.Unstarted++
		case evergreen.TaskStarted:
			out.Started++
		case evergreen.TaskSucceeded:
			out.Succeeded++
		case evergreen.TaskFailed:
			out.Failed++
		case evergreen.TaskSystemFailed:
			out.SystemFailed++
		case evergreen.TaskSystemUnresponse:
			out.SystemUnresponsive++
		case evergreen.TaskSystemTimedOut:
			out.SystemTimedOut++
		case evergreen.TaskTestTimedOut:
			out.TestTimedOut++
		}
	}

	if out.Total > 0 {
		out.loggable = true
	}

	return &out
}

func (c *ResultCounts) Raw() interface{} { c.Collect(); return c } // nolint: golint
func (c *ResultCounts) Loggable() bool   { return c.loggable }     // nolint: golint
func (c *ResultCounts) String() string { // nolint: golint
	if !c.Loggable() {
		return ""
	}

	if c.cachedMessage == "" {
		out, _ := json.Marshal(c)
		c.cachedMessage = string(out)
	}

	return c.cachedMessage
}
