package main

import (
	"fmt"

	"github.com/geraldhinson/siftd-base/pkg/helpers"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
	"github.com/geraldhinson/siftd-example-employee-service/routers"
)

func main() {
	// call setup to get the service base (logging, config, routing) and a keystore
	employeeService := serviceBase.NewServiceBase()
	if employeeService == nil {
		fmt.Println("Failed to validate configuration and listen. Shutting down.")
		return
	}

	// the only router we need to write is the noun router
	NounRouter := routers.NewNounRouter(employeeService)
	if NounRouter == nil {
		employeeService.Logger.Fatalf("Failed to create noun api server. Shutting down.")
	}

	// here we use a default implementation, but must pass auth model
	NounJournalRouter := helpers.NewNounJournalRouter[models.EmployeeResource](employeeService, security.REALM_MACHINE, security.VALID_IDENTITY, security.ONE_HOUR, nil)
	if NounJournalRouter == nil {
		employeeService.Logger.Fatalf("Failed to create journal api server. Shutting down.")
	}

	// here we use a default implementation, but must pass auth model
	HealthCheckRouter := helpers.NewNounHealthCheckRouter[models.EmployeeResource](employeeService, security.NO_REALM, security.NO_AUTH, security.NO_EXPIRY, nil)
	if HealthCheckRouter == nil {
		employeeService.Logger.Fatalf("Failed to create health check api server. Shutting down.")
	}

	// [Optional] Add fake identity endpoints for local loopback testing if not using a separate identity service.
	// This will fail if the interface is not defined as loopback
	if employeeService.IsLoopbackListener() {
		FakeIdentityServiceRouter := helpers.NewFakeIdentityServiceRouter(employeeService, security.NO_REALM, security.NO_AUTH, security.NO_EXPIRY, nil)
		if FakeIdentityServiceRouter == nil {
			employeeService.Logger.Fatalf(
				"Failed to create fake identity service api server (for testing only). Shutting down.",
			)
		}
	}

	// starting listening for incoming API calls
	employeeService.ListenAndServe()
}
