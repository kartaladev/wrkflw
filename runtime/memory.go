package runtime

import "github.com/zakyalvan/krtlwrkflw/engine"

// Compile-time checks that the in-memory fakes satisfy the ports.
var (
	_ StateStore   = (*MemStateStore)(nil)
	_ Journal      = (*MemJournal)(nil)
	_ OutboxWriter = (*MemOutbox)(nil)
)

// MemStateStore is an in-memory StateStore for tests and reference wiring.
type MemStateStore struct{ m map[string]engine.InstanceState }

func NewMemStateStore() *MemStateStore { return &MemStateStore{m: map[string]engine.InstanceState{}} }

func (s *MemStateStore) Load(id string) (engine.InstanceState, error) {
	st, ok := s.m[id]
	if !ok {
		return engine.InstanceState{}, ErrInstanceNotFound
	}
	return st, nil
}

func (s *MemStateStore) Save(st engine.InstanceState) error {
	s.m[st.InstanceID] = st
	return nil
}

// MemJournal is an in-memory Journal (and JournalReader) for tests and reference wiring.
type MemJournal struct{ m map[string][]engine.Trigger }

func NewMemJournal() *MemJournal { return &MemJournal{m: map[string][]engine.Trigger{}} }

func (j *MemJournal) Append(id string, trg engine.Trigger) error {
	j.m[id] = append(j.m[id], trg)
	return nil
}

func (j *MemJournal) Entries(id string) []engine.Trigger { return j.m[id] }

// MemOutbox is an in-memory OutboxWriter for tests and reference wiring.
type MemOutbox struct{ events []OutboxEvent }

func NewMemOutbox() *MemOutbox { return &MemOutbox{} }

func (o *MemOutbox) Write(topic string, payload map[string]any) error {
	o.events = append(o.events, OutboxEvent{Topic: topic, Payload: payload})
	return nil
}

// Events returns the recorded outbox events.
func (o *MemOutbox) Events() []OutboxEvent { return o.events }
