package routers

import (
	"github.com/geraldhinson/siftd-base/pkg/helpers"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
)

type NounJournalRouter struct {
	*helpers.NounJournalRoutesHelper[models.EmployeeResource]
}

func NewNounJournalRouter(employeeService *serviceBase.ServiceBase) *NounJournalRouter {
	employeeService.Logger.Info("Setting up the noun journal router")

	// This router is mostly built using serviceBase implementation, but we don't fully implement it in
	// serviceBase because:
	// 1. We want it to be obvious that it is one of the routers this service implements (ie.
	//    easily seen in the routers folder)
	// 2. It is important for the service writer to define the auth model for all routers
	//
	authModelMachine, err := employeeService.NewAuthModel(security.REALM_MACHINE, security.VALID_IDENTITY, security.ONE_HOUR, nil)
	if err != nil {
		employeeService.Logger.Fatalf("Failed to initialize AuthModelMachine in SecuredQueriesRouter : %v", err)
		return nil
	}

	// OK. Auth is defined. Now use the helper code to do the rest of the heavy lifting here.
	//
	NounJournalRoutesHelper := helpers.NewNounJournalRoutesHelper[models.EmployeeResource](employeeService, authModelMachine)
	if NounJournalRoutesHelper == nil {
		employeeService.Logger.Println("Error creating NounJournalRoutesHelper")
		return nil
	}

	NounJournalServiceRouter := &NounJournalRouter{
		NounJournalRoutesHelper: NounJournalRoutesHelper,
	}

	return NounJournalServiceRouter
}
