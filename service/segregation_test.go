package service_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/service"
)

var (
	_ service.InstanceStarter = (*service.ProcessEngine)(nil)
	_ service.InstanceReader  = (*service.ProcessEngine)(nil)
	_ service.TaskManager     = (*service.ProcessEngine)(nil)
	_ service.Messaging       = (*service.ProcessEngine)(nil)
	_ service.InstanceOps     = (*service.ProcessEngine)(nil)
	_ service.Service         = (*service.ProcessEngine)(nil)
)

func TestEngineSatisfiesRoleInterfaces(t *testing.T) {
	e, err := service.NewProcessEngine()
	if err != nil {
		t.Fatal(err)
	}
	var _ service.Service = e
}
