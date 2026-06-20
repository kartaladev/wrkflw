package engine

// Command is the sealed set of side effects the core asks the runtime to
// perform. The unexported marker keeps the set closed.
type Command interface {
	isCommand()
}

// InvokeAction asks the runtime to run a named ServiceAction. Its result
// returns as an ActionCompleted/ActionFailed trigger carrying the same CommandID.
type InvokeAction struct {
	CommandID string
	Name      string
	Input     map[string]any
}

// CompleteInstance marks the instance complete with a result.
type CompleteInstance struct {
	Result map[string]any
}

// FailInstance marks the instance failed.
type FailInstance struct {
	Err string
}

func (InvokeAction) isCommand()     {}
func (CompleteInstance) isCommand() {}
func (FailInstance) isCommand()     {}
