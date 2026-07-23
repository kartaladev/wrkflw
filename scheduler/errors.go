package scheduler

import "errors"

// ErrJobNotFound is returned when a job lookup (by ID) finds no matching job.
var ErrJobNotFound = errors.New("workflow-scheduler: job not found")

// ErrUnresolvedTimerDefinitions is returned (wrapped) when some scheduled
// jobs reference process/timer definitions that could not be resolved at
// rehydration time. It is the home of this sentinel; earlier releases
// exposed a runtime/kernel copy, now removed.
var ErrUnresolvedTimerDefinitions = errors.New("workflow-scheduler: some scheduled jobs reference unresolved definitions")

// ErrUnsupportedTrigger is returned when a [Trigger] carries a kind the
// scheduling backend in use cannot honor. It is the home of this sentinel;
// earlier releases exposed a runtime/kernel copy, now removed.
var ErrUnsupportedTrigger = errors.New("workflow-scheduler: unsupported trigger")
