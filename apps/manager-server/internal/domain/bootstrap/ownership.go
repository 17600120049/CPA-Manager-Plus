package bootstrap

import "errors"

type ComponentOwner string

const (
	ManagerServerOwner     ComponentOwner = "manager_server"
	RuntimeSupervisorOwner ComponentOwner = "runtime_supervisor"
)

type OwnershipContract struct {
	DatabaseOwner            ComponentOwner
	DataKeyOwner             ComponentOwner
	MigrationOwner           ComponentOwner
	ProductCheckpointOwner   ComponentOwner
	ManagedCPALifecycleOwner ComponentOwner
}

func RequiredOwnershipContract() OwnershipContract {
	return OwnershipContract{
		DatabaseOwner:            ManagerServerOwner,
		DataKeyOwner:             ManagerServerOwner,
		MigrationOwner:           ManagerServerOwner,
		ProductCheckpointOwner:   ManagerServerOwner,
		ManagedCPALifecycleOwner: RuntimeSupervisorOwner,
	}
}

func (c OwnershipContract) Validate() error {
	if c != RequiredOwnershipContract() {
		return errors.New("Manager/Supervisor ownership boundary drifted")
	}
	return nil
}
