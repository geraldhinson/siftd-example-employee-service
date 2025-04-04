package routers

import (
	"github.com/geraldhinson/siftd-base/pkg/helpers"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
)

type HealthCheckRouter struct {
	*helpers.HealthCheckRoutesHelper[models.EmployeeResource]
}

func NewHealthCheckRouter(employeeService *serviceBase.ServiceBase) *HealthCheckRouter {

	// This router is mostly built using serviceBase implementation, but we don't fully implement it in
	// serviceBase because:
	// 1. We want it to be obvious that it is one of the routers this service implements (ie.
	//    easily seen in the routers folder)
	// 2. It is important for the service writer to define the auth model for all routers
	//
	var authModelUsers = employeeService.NewAuthModel(security.ONE_DAY, []security.AuthTypes{security.NO_AUTH}, nil)
	if authModelUsers == nil {
		employeeService.Logger.Fatalf("Failed to initialize AuthModelUsers in HealthCheckServiceRouter")
	}

	// OK. Auth is defined. Now use the helper code to do the rest of the heavy lifting here.
	//
	HealthCheckRoutesHelper := helpers.NewHealthCheckRoutesHelper[models.EmployeeResource](employeeService, authModelUsers)
	if HealthCheckRoutesHelper == nil {
		employeeService.Logger.Println("Error creating HealthCheckRoutesHelper")
		return nil
	}

	HealthCheckServiceRouter := &HealthCheckRouter{
		HealthCheckRoutesHelper: HealthCheckRoutesHelper,
	}

	return HealthCheckServiceRouter
}
