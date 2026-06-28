package model

// AddStartEvent adds a StartEvent node to the definition. See NewStartEvent.
func (b *DefinitionBuilder) AddStartEvent(id string, opts ...startEventOption) *DefinitionBuilder {
	return b.Add(NewStartEvent(id, opts...))
}

// AddEndEvent adds an EndEvent node to the definition. See NewEndEvent.
func (b *DefinitionBuilder) AddEndEvent(id string, name ...string) *DefinitionBuilder {
	return b.Add(NewEndEvent(id, name...))
}

// AddTerminateEndEvent adds a TerminateEndEvent node to the definition. See NewTerminateEndEvent.
func (b *DefinitionBuilder) AddTerminateEndEvent(id string, name ...string) *DefinitionBuilder {
	return b.Add(NewTerminateEndEvent(id, name...))
}

// AddErrorEndEvent adds an ErrorEndEvent node to the definition. See NewErrorEndEvent.
func (b *DefinitionBuilder) AddErrorEndEvent(id, errorCode string, name ...string) *DefinitionBuilder {
	return b.Add(NewErrorEndEvent(id, errorCode, name...))
}

// AddExclusiveGateway adds an ExclusiveGateway node to the definition. See NewExclusiveGateway.
func (b *DefinitionBuilder) AddExclusiveGateway(id string, name ...string) *DefinitionBuilder {
	return b.Add(NewExclusiveGateway(id, name...))
}

// AddParallelGateway adds a ParallelGateway node to the definition. See NewParallelGateway.
func (b *DefinitionBuilder) AddParallelGateway(id string, name ...string) *DefinitionBuilder {
	return b.Add(NewParallelGateway(id, name...))
}

// AddInclusiveGateway adds an InclusiveGateway node to the definition. See NewInclusiveGateway.
func (b *DefinitionBuilder) AddInclusiveGateway(id string, name ...string) *DefinitionBuilder {
	return b.Add(NewInclusiveGateway(id, name...))
}

// AddEventBasedGateway adds an EventBasedGateway node to the definition. See NewEventBasedGateway.
func (b *DefinitionBuilder) AddEventBasedGateway(id string, name ...string) *DefinitionBuilder {
	return b.Add(NewEventBasedGateway(id, name...))
}

// AddServiceTask adds a ServiceTask node to the definition. See NewServiceTask.
func (b *DefinitionBuilder) AddServiceTask(id string, opts ...serviceTaskOption) *DefinitionBuilder {
	return b.Add(NewServiceTask(id, opts...))
}

// AddUserTask adds a UserTask node to the definition. See NewUserTask.
func (b *DefinitionBuilder) AddUserTask(id string, roles []string, opts ...userTaskOption) *DefinitionBuilder {
	return b.Add(NewUserTask(id, roles, opts...))
}

// AddReceiveTask adds a ReceiveTask node to the definition. See NewReceiveTask.
func (b *DefinitionBuilder) AddReceiveTask(id, messageName string, opts ...receiveTaskOption) *DefinitionBuilder {
	return b.Add(NewReceiveTask(id, messageName, opts...))
}

// AddSendTask adds a SendTask node to the definition. See NewSendTask.
func (b *DefinitionBuilder) AddSendTask(id, messageName string, opts ...sendTaskOption) *DefinitionBuilder {
	return b.Add(NewSendTask(id, messageName, opts...))
}

// AddBusinessRuleTask adds a BusinessRuleTask node to the definition. See NewBusinessRuleTask.
func (b *DefinitionBuilder) AddBusinessRuleTask(id string, opts ...businessRuleOption) *DefinitionBuilder {
	return b.Add(NewBusinessRuleTask(id, opts...))
}

// AddSubProcess adds a SubProcess node to the definition. See NewSubProcess.
func (b *DefinitionBuilder) AddSubProcess(id string, sub *ProcessDefinition, opts ...activityOption) *DefinitionBuilder {
	return b.Add(NewSubProcess(id, sub, opts...))
}

// AddCallActivity adds a CallActivity node to the definition. See NewCallActivity.
func (b *DefinitionBuilder) AddCallActivity(id, defRef string, opts ...activityOption) *DefinitionBuilder {
	return b.Add(NewCallActivity(id, defRef, opts...))
}

// AddEventSubProcess adds an EventSubProcess node to the definition. See NewEventSubProcess.
func (b *DefinitionBuilder) AddEventSubProcess(id string, sub *ProcessDefinition, opts ...eventSubProcessOption) *DefinitionBuilder {
	return b.Add(NewEventSubProcess(id, sub, opts...))
}

// AddIntermediateCatchEvent adds an IntermediateCatchEvent node to the definition. See NewIntermediateCatchEvent.
func (b *DefinitionBuilder) AddIntermediateCatchEvent(id string, opts ...catchOption) *DefinitionBuilder {
	return b.Add(NewIntermediateCatchEvent(id, opts...))
}

// AddIntermediateThrowEvent adds an IntermediateThrowEvent node to the definition. See NewIntermediateThrowEvent.
func (b *DefinitionBuilder) AddIntermediateThrowEvent(id string, opts ...throwOption) *DefinitionBuilder {
	return b.Add(NewIntermediateThrowEvent(id, opts...))
}

// AddBoundaryEvent adds a BoundaryEvent node to the definition. See NewBoundaryEvent.
func (b *DefinitionBuilder) AddBoundaryEvent(id, attachedTo string, opts ...boundaryOption) *DefinitionBuilder {
	return b.Add(NewBoundaryEvent(id, attachedTo, opts...))
}
