package model

// AddStartEvent adds a StartEvent node to the definition. See NewStartEvent.
func (b *definitionBuilder) AddStartEvent(id string, opts ...startEventOption) DefinitionBuilder {
	return b.Add(NewStartEvent(id, opts...))
}

// AddEndEvent adds an EndEvent node to the definition. See NewEndEvent.
func (b *definitionBuilder) AddEndEvent(id string, name ...string) DefinitionBuilder {
	return b.Add(NewEndEvent(id, name...))
}

// AddTerminateEndEvent adds a TerminateEndEvent node to the definition. See NewTerminateEndEvent.
func (b *definitionBuilder) AddTerminateEndEvent(id string, name ...string) DefinitionBuilder {
	return b.Add(NewTerminateEndEvent(id, name...))
}

// AddErrorEndEvent adds an ErrorEndEvent node to the definition. See NewErrorEndEvent.
func (b *definitionBuilder) AddErrorEndEvent(id, errorCode string, name ...string) DefinitionBuilder {
	return b.Add(NewErrorEndEvent(id, errorCode, name...))
}

// AddExclusiveGateway adds an ExclusiveGateway node to the definition. See NewExclusiveGateway.
func (b *definitionBuilder) AddExclusiveGateway(id string, name ...string) DefinitionBuilder {
	return b.Add(NewExclusiveGateway(id, name...))
}

// AddParallelGateway adds a ParallelGateway node to the definition. See NewParallelGateway.
func (b *definitionBuilder) AddParallelGateway(id string, name ...string) DefinitionBuilder {
	return b.Add(NewParallelGateway(id, name...))
}

// AddInclusiveGateway adds an InclusiveGateway node to the definition. See NewInclusiveGateway.
func (b *definitionBuilder) AddInclusiveGateway(id string, name ...string) DefinitionBuilder {
	return b.Add(NewInclusiveGateway(id, name...))
}

// AddEventBasedGateway adds an EventBasedGateway node to the definition. See NewEventBasedGateway.
func (b *definitionBuilder) AddEventBasedGateway(id string, name ...string) DefinitionBuilder {
	return b.Add(NewEventBasedGateway(id, name...))
}

// AddServiceTask adds a ServiceTask node to the definition. See NewServiceTask.
func (b *definitionBuilder) AddServiceTask(id string, opts ...serviceTaskOption) DefinitionBuilder {
	return b.Add(NewServiceTask(id, opts...))
}

// AddUserTask adds a UserTask node to the definition. See NewUserTask.
func (b *definitionBuilder) AddUserTask(id string, roles []string, opts ...userTaskOption) DefinitionBuilder {
	return b.Add(NewUserTask(id, roles, opts...))
}

// AddReceiveTask adds a ReceiveTask node to the definition. See NewReceiveTask.
func (b *definitionBuilder) AddReceiveTask(id, messageName string, opts ...receiveTaskOption) DefinitionBuilder {
	return b.Add(NewReceiveTask(id, messageName, opts...))
}

// AddSendTask adds a SendTask node to the definition. See NewSendTask.
func (b *definitionBuilder) AddSendTask(id, messageName string, opts ...sendTaskOption) DefinitionBuilder {
	return b.Add(NewSendTask(id, messageName, opts...))
}

// AddBusinessRuleTask adds a BusinessRuleTask node to the definition. See NewBusinessRuleTask.
func (b *definitionBuilder) AddBusinessRuleTask(id string, opts ...businessRuleOption) DefinitionBuilder {
	return b.Add(NewBusinessRuleTask(id, opts...))
}

// AddSubProcess adds a SubProcess node to the definition. See NewSubProcess.
func (b *definitionBuilder) AddSubProcess(id string, sub *ProcessDefinition, opts ...activityOption) DefinitionBuilder {
	return b.Add(NewSubProcess(id, sub, opts...))
}

// AddCallActivity adds a CallActivity node to the definition. See NewCallActivity.
func (b *definitionBuilder) AddCallActivity(id, defRef string, opts ...activityOption) DefinitionBuilder {
	return b.Add(NewCallActivity(id, defRef, opts...))
}

// AddEventSubProcess adds an EventSubProcess node to the definition. See NewEventSubProcess.
func (b *definitionBuilder) AddEventSubProcess(id string, sub *ProcessDefinition, opts ...eventSubProcessOption) DefinitionBuilder {
	return b.Add(NewEventSubProcess(id, sub, opts...))
}

// AddIntermediateCatchEvent adds an IntermediateCatchEvent node to the definition. See NewIntermediateCatchEvent.
func (b *definitionBuilder) AddIntermediateCatchEvent(id string, opts ...catchOption) DefinitionBuilder {
	return b.Add(NewIntermediateCatchEvent(id, opts...))
}

// AddIntermediateThrowEvent adds an IntermediateThrowEvent node to the definition. See NewIntermediateThrowEvent.
func (b *definitionBuilder) AddIntermediateThrowEvent(id string, opts ...throwOption) DefinitionBuilder {
	return b.Add(NewIntermediateThrowEvent(id, opts...))
}

// AddBoundaryEvent adds a BoundaryEvent node to the definition. See NewBoundaryEvent.
func (b *definitionBuilder) AddBoundaryEvent(id, attachedTo string, opts ...boundaryOption) DefinitionBuilder {
	return b.Add(NewBoundaryEvent(id, attachedTo, opts...))
}
