package service_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/service"
)

var (
	_ service.InstanceStarter = (*service.Engine)(nil)
	_ service.InstanceReader  = (*service.Engine)(nil)
	_ service.TaskManager     = (*service.Engine)(nil)
	_ service.Messaging       = (*service.Engine)(nil)
	_ service.InstanceOps     = (*service.Engine)(nil)
	_ service.Service         = (*service.Engine)(nil)
)

func TestEngineSatisfiesRoleInterfaces(t *testing.T) {
	e, err := service.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	var _ service.Service = e
}
