package routers

import (
	"github.com/geraldhinson/siftd-base/pkg/helpers"
	"github.com/geraldhinson/siftd-base/pkg/resourceStore"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
)

type NounJournalRouter struct {
	*helpers.NounJournalRoutesHelper[models.EmployeeResource]
}

func NewNounJournalRouter(employeeService *serviceBase.ServiceBase) *NounJournalRouter {

	// This router is mostly built using serviceBase implementation, but we don't fully implement it in
	// serviceBase because:
	// 1. We want it to be obvious that it is one of the routers this service implements (ie.
	//    easily seen in the routers folder)
	// 2. It is important for the service writer to define the auth model for all routers
	// 3. We need to instantiate the resource store with the correct type (EmployeeResource) for this service
	//
	var authModelUsers = employeeService.NewAuthModel(security.ONE_DAY, []security.AuthTypes{security.NO_AUTH}, nil)
	if authModelUsers == nil {
		employeeService.Logger.Fatalf("Failed to initialize AuthModelUsers in NounJournalServiceRouter")
	}

	store, err := resourceStore.NewPostgresResourceStoreWithJournal[models.EmployeeResource](
		employeeService.Configuration,
		employeeService.Logger)
	if err != nil {
		employeeService.Logger.Println("Error creating PostgresResourceStoreWithJournal:", err)
		return nil
	}

	// OK. Auth is defined. Now use the helper code to do the rest of the heavy lifting here.
	//
	NounJournalRoutesHelper := helpers.NewNounJournalRoutesHelper(employeeService, authModelUsers, store)
	if NounJournalRoutesHelper == nil {
		employeeService.Logger.Println("Error creating NounJournalRoutesHelper")
		return nil
	}

	NounJournalServiceRouter := &NounJournalRouter{
		NounJournalRoutesHelper: NounJournalRoutesHelper,
	}

	return NounJournalServiceRouter
}
