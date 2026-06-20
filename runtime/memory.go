package runtime

import "github.com/zakyalvan/krtlwrkflw/engine"

type memStateStore struct{ m map[string]engine.InstanceState }

func NewMemStateStore() StateStore { return &memStateStore{m: map[string]engine.InstanceState{}} }

func (s *memStateStore) Load(id string) (engine.InstanceState, bool) {
	st, ok := s.m[id]
	return st, ok
}
func (s *memStateStore) Save(st engine.InstanceState) { s.m[st.InstanceID] = st }

type memJournal struct{ m map[string][]engine.Trigger }

func NewMemJournal() *memJournal { return &memJournal{m: map[string][]engine.Trigger{}} }

func (j *memJournal) Append(id string, trg engine.Trigger) { j.m[id] = append(j.m[id], trg) }
func (j *memJournal) Entries(id string) []engine.Trigger   { return j.m[id] }

type memOutbox struct {
	events []struct {
		Topic   string
		Payload map[string]any
	}
}

func NewMemOutbox() *memOutbox { return &memOutbox{} }

func (o *memOutbox) Write(topic string, payload map[string]any) {
	o.events = append(o.events, struct {
		Topic   string
		Payload map[string]any
	}{topic, payload})
}
